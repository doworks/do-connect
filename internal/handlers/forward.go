package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// Forwarding sends a copy of one message to other chats. WhatsApp's API has no
// "forwarded" marker, so patients get an ordinary message; staff see it marked.

const maxForwardTargets = 10

var forwardableMedia = map[models.MessageType]bool{
	models.MessageTypeImage:    true,
	models.MessageTypeVideo:    true,
	models.MessageTypeAudio:    true,
	models.MessageTypeDocument: true,
}

type ForwardRequest struct {
	ContactIDs []uuid.UUID `json:"contact_ids"`
}

type ForwardResult struct {
	ContactID uuid.UUID `json:"contact_id"`
	Status    string    `json:"status"` // sent, held, failed
	Reason    string    `json:"reason,omitempty"`
	MessageID uuid.UUID `json:"message_id,omitempty"`
}

// ForwardMessage sends a copy of a message to up to ten chats.
func (a *App) ForwardMessage(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	messageID, err := parsePathUUID(r, "message_id", "message")
	if err != nil {
		return nil
	}
	var req ForwardRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil || len(req.ContactIDs) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Choose at least one chat", nil, "")
	}
	if len(req.ContactIDs) > maxForwardTargets {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Forward to at most ten chats at once", nil, "")
	}

	source, err := findByIDAndOrg[models.Message](a.DB, r, messageID, orgID, "Message")
	if err != nil {
		return nil
	}
	// Only from a chat this person can see.
	if !a.HasPermission(userID, models.ResourceContacts, models.ActionRead, orgID) {
		var count int64
		a.scopeAssignedContact(a.DB.Model(&models.Contact{}).Where("id = ? AND organization_id = ?", source.ContactID, orgID), userID, orgID).Count(&count)
		if count == 0 {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Message not found", nil, "")
		}
	}

	text, media, reason := forwardContent(source)
	if reason != "" {
		return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, reason, nil, "")
	}
	var mediaData []byte
	if media {
		if mediaData, err = a.readStoredMedia(source.MediaURL); err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, "This file isn't saved in Whatomate, so it can't be forwarded", nil, "")
		}
	}

	results := make([]ForwardResult, 0, len(req.ContactIDs))
	seen := map[uuid.UUID]bool{}
	for _, contactID := range req.ContactIDs {
		if seen[contactID] {
			continue
		}
		seen[contactID] = true
		results = append(results, a.forwardTo(r, orgID, userID, source, contactID, text, media, mediaData))
	}
	return r.SendEnvelope(map[string]any{"results": results})
}

// forwardContent says what to send: the text, whether the stored file goes too,
// or why this message can't be forwarded.
func forwardContent(source *models.Message) (text string, media bool, reason string) {
	switch {
	case source.Status == models.MessageStatusHeld:
		return "", false, "This message hasn't been sent yet"
	case forwardableMedia[source.MessageType]:
		if source.MediaURL == "" {
			return "", false, "This file isn't saved in Whatomate, so it can't be forwarded"
		}
		return source.Content, true, ""
	case source.MessageType == models.MessageTypeReaction || source.MessageType == models.MessageTypeCall:
		return "", false, "This kind of message can't be forwarded"
	}
	// Text, and the visible text of templates, buttons and replies.
	if source.Content == "" {
		return "", false, "This message has no text to forward"
	}
	return source.Content, false, ""
}

func (a *App) forwardTo(r *fastglue.Request, orgID, userID uuid.UUID, source *models.Message, contactID uuid.UUID, text string, media bool, mediaData []byte) ForwardResult {
	result := ForwardResult{ContactID: contactID, Status: "failed"}
	var contact models.Contact
	query := a.scopeAssignedContact(a.DB.Where("id = ? AND organization_id = ?", contactID, orgID), userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		result.Reason = "Chat not found"
		return result
	}
	account, err := a.resolveWhatsAppAccount(orgID, contact.WhatsAppAccount)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	metadata := models.JSONB{"forwarded_from": source.ID.String()}

	if !serviceWindowOpen(&contact) {
		if media {
			result.Reason = "They haven't written in 24 hours. Photos and files can only go once they message the clinic."
			return result
		}
		template := a.unlockTemplate(&contact, account)
		if template == nil {
			result.Reason = "They haven't written in 24 hours, so only a template can be sent."
			return result
		}
		held, err := a.holdText(&contact, account, template, text, userID, metadata)
		if err != nil {
			result.Reason = "Could not save the message"
			return result
		}
		result.Status, result.MessageID = "held", held.ID
		return result
	}

	msgReq := OutgoingMessageRequest{Account: account, Contact: &contact, Type: models.MessageTypeText, Content: text}
	if media {
		msgReq = OutgoingMessageRequest{
			Account:       account,
			Contact:       &contact,
			Type:          source.MessageType,
			MediaData:     mediaData,
			MediaURL:      source.MediaURL,
			MediaMimeType: baseMime(source.MediaMimeType),
			MediaFilename: mediaFilename(source),
			Caption:       text,
		}
	}
	opts := DefaultSendOptions()
	a.setSender(r, &opts, userID, "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sent, err := a.SendOutgoingMessage(ctx, msgReq, opts)
	if sent != nil {
		marker, _ := json.Marshal(metadata)
		a.DB.Model(&models.Message{}).Where("id = ?", sent.ID).
			Update("metadata", gorm.Expr("COALESCE(metadata, '{}'::jsonb) || ?::jsonb", string(marker)))
		result.MessageID = sent.ID
	}
	if err != nil {
		a.Log.Error("Forward failed", "error", err, "contact_id", contact.ID)
		result.Reason = "WhatsApp refused this message"
		return result
	}
	if sent != nil && sent.Status == models.MessageStatusFailed {
		result.Reason = sent.ErrorMessage
		return result
	}
	result.Status = "sent"
	return result
}
