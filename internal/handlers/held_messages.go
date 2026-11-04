package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Outside WhatsApp's 24-hour window only templates get through. Staff still type
// as usual: the message is held, the patient gets a short unlock template, and
// their reply opens the window and releases what's waiting.

const (
	serviceWindow = 24 * time.Hour
	// A held message older than this is out of date, so it isn't sent when the
	// patient finally writes.
	heldMessageMaxAge = 7 * 24 * time.Hour
)

func serviceWindowOpen(contact *models.Contact) bool {
	return contact.LastInboundAt != nil && time.Since(*contact.LastInboundAt) < serviceWindow
}

// unlockTemplateNames returns the configured template names by language.
func (a *App) unlockTemplateNames(orgID uuid.UUID) (english, arabic string) {
	var org models.Organization
	if err := a.DB.Select("settings").Where("id = ?", orgID).First(&org).Error; err != nil || org.Settings == nil {
		return "", ""
	}
	english, _ = org.Settings["unlock_template_en"].(string)
	arabic, _ = org.Settings["unlock_template_ar"].(string)
	return english, arabic
}

func contactPrefersArabic(contact *models.Contact) bool {
	if strings.HasPrefix(contact.ProfileName, "AR · ") {
		return true
	}
	for _, tag := range contact.Tags {
		if tag == "Arabic" {
			return true
		}
	}
	return false
}

// unlockTemplate picks the approved template in the patient's language. With no
// Arabic one the whole message is in English, because one message is never mixed.
func (a *App) unlockTemplate(contact *models.Contact, account *models.WhatsAppAccount) *models.Template {
	english, arabic := a.unlockTemplateNames(account.OrganizationID)
	names := []string{english}
	if contactPrefersArabic(contact) {
		names = []string{arabic, english}
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		var template models.Template
		err := a.DB.Where("organization_id = ? AND whats_app_account = ? AND name = ? AND status = ?",
			account.OrganizationID, account.Name, name, "APPROVED").First(&template).Error
		if err == nil {
			return &template
		}
	}
	return nil
}

// holdTextMessage saves the message and asks the patient to open the window.
func (a *App) holdTextMessage(r *fastglue.Request, contact *models.Contact, account *models.WhatsAppAccount, template *models.Template, body string, userID uuid.UUID) error {
	message, err := a.holdText(contact, account, template, body, userID, nil)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to save the message", nil, "")
	}
	return r.SendEnvelope(heldResponse(message))
}

func heldResponse(message *models.Message) MessageResponse {
	response := MessageResponse{
		ID:              message.ID,
		ContactID:       message.ContactID,
		Direction:       message.Direction,
		MessageType:     message.MessageType,
		Content:         map[string]string{"body": message.Content},
		MediaURL:        messageMediaURL(message),
		MediaMimeType:   message.MediaMimeType,
		MediaFilename:   message.MediaFilename,
		Status:          message.Status,
		WhatsAppAccount: message.WhatsAppAccount,
		CreatedAt:       message.CreatedAt,
		UpdatedAt:       message.UpdatedAt,
	}
	applySenderFields(&response, message)
	return response
}

// holdText saves a text to go out when the patient replies, and sends the unlock
// template unless one went out recently.
func (a *App) holdText(contact *models.Contact, account *models.WhatsAppAccount, template *models.Template, body string, userID uuid.UUID, metadata models.JSONB) (*models.Message, error) {
	return a.hold(contact, account, template, &models.Message{
		MessageType: models.MessageTypeText,
		Content:     body,
		Metadata:    metadata,
	}, userID, truncateString(body, 100))
}

// holdMedia does the same for a photo or file already saved in Whatomate. It
// keeps the file's name and type, so it goes out as it would have now.
func (a *App) holdMedia(contact *models.Contact, account *models.WhatsAppAccount, template *models.Template, kind models.MessageType, caption, mediaURL, mimeType, filename string, userID uuid.UUID, metadata models.JSONB) (*models.Message, error) {
	preview := a.getMessagePreview(OutgoingMessageRequest{Type: kind, Caption: caption, MediaFilename: filename})
	return a.hold(contact, account, template, &models.Message{
		MessageType:   kind,
		Content:       caption,
		MediaURL:      mediaURL,
		MediaMimeType: mimeType,
		MediaFilename: filename,
		Metadata:      metadata,
	}, userID, preview)
}

func (a *App) hold(contact *models.Contact, account *models.WhatsAppAccount, template *models.Template, message *models.Message, userID uuid.UUID, preview string) (*models.Message, error) {
	message.ID = uuid.New()
	message.OrganizationID = account.OrganizationID
	message.WhatsAppAccount = account.Name
	message.ContactID = contact.ID
	message.Direction = models.DirectionOutgoing
	message.Status = models.MessageStatusHeld
	message.SentByUserID = &userID
	if err := a.DB.Create(message).Error; err != nil {
		a.Log.Error("Failed to hold message", "error", err)
		return nil, err
	}
	message.SentByUser = a.senderUser(userID)
	a.updateContactLastMessage(contact, preview)
	a.broadcastNewMessage(account.OrganizationID, message, contact)

	// A second held message doesn't send a second notice.
	if !a.unlockRecentlySent(contact.ID, template.Name) {
		opts := DefaultSendOptions()
		opts.SentByUserID = &userID
		if _, err := a.SendOutgoingMessage(context.Background(), OutgoingMessageRequest{
			Account:  account,
			Contact:  contact,
			Type:     models.MessageTypeTemplate,
			Template: template,
		}, opts); err != nil {
			a.Log.Error("Failed to send unlock template", "error", err, "contact_id", contact.ID)
		}
	}
	return message, nil
}

func (a *App) unlockRecentlySent(contactID uuid.UUID, templateName string) bool {
	var count int64
	a.DB.Model(&models.Message{}).
		Where("contact_id = ? AND template_name = ? AND status <> ? AND created_at > ?",
			contactID, templateName, models.MessageStatusFailed, time.Now().Add(-serviceWindow)).
		Count(&count)
	return count > 0
}

// releaseHeldMessages sends what was waiting, oldest first. Anything the patient
// wrote opens the window. Tapping the unlock button isn't a question for staff,
// so that tap doesn't leave the chat unread or waiting.
func (a *App) releaseHeldMessages(account *models.WhatsAppAccount, contact *models.Contact, trigger *models.Message) {
	var held []models.Message
	if err := a.DB.Where("contact_id = ? AND direction = ? AND status = ?", contact.ID, models.DirectionOutgoing, models.MessageStatusHeld).
		Order("created_at ASC").Find(&held).Error; err != nil || len(held) == 0 {
		return
	}

	if a.isUnlockTap(account, trigger) {
		a.DB.Model(trigger).Update("status", models.MessageStatusRead)
		a.DB.Model(contact).Updates(map[string]any{"waiting_since": nil, "is_read": true})
		a.broadcastReadState(account.OrganizationID, contact.ID)
	}

	// Sending updates the contact it's given, and the caller is still using this one.
	own := *contact
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for i := range held {
			a.releaseHeldMessage(account, &own, &held[i])
		}
	}()
}

func (a *App) releaseHeldMessage(account *models.WhatsAppAccount, contact *models.Contact, message *models.Message) {
	if time.Since(message.CreatedAt) > heldMessageMaxAge {
		a.finishHeldAsFailed(account, message, "Not sent: they didn't reply within 7 days")
		return
	}
	// Two patient messages at once must not send it twice. The new time puts it
	// where the patient gets it: after their reply.
	claim := a.DB.Model(&models.Message{}).
		Where("id = ? AND status = ?", message.ID, models.MessageStatusHeld).
		Updates(map[string]any{"status": models.MessageStatusPending, "created_at": time.Now()})
	if claim.Error != nil || claim.RowsAffected == 0 {
		return
	}
	message.Status = models.MessageStatusPending

	req := OutgoingMessageRequest{
		Account: account,
		Contact: contact,
		Type:    message.MessageType,
		Content: message.Content,
	}
	// A photo or file goes from the copy saved when it was written. Meta wants a
	// name and a plain type, which a patient's own file may not have.
	if forwardableMedia[message.MessageType] {
		data, err := a.readStoredMedia(message.MediaURL)
		if err != nil {
			a.finishHeldAsFailed(account, message, "Not sent: the file is no longer saved")
			return
		}
		req.MediaData = data
		req.MediaURL = message.MediaURL
		req.MediaMimeType = baseMime(message.MediaMimeType)
		req.MediaFilename = mediaFilename(message)
		req.Caption = message.Content
	}

	opts := DefaultSendOptions()
	opts.Async = false
	opts.SentByUserID = message.SentByUserID
	opts.ExistingMessage = message
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := a.SendOutgoingMessage(ctx, req, opts); err != nil {
		a.Log.Error("Failed to release held message", "error", err, "message_id", message.ID)
	}
}

func (a *App) finishHeldAsFailed(account *models.WhatsAppAccount, message *models.Message, reason string) {
	a.DB.Model(&models.Message{}).Where("id = ?", message.ID).Updates(map[string]any{
		"status":        models.MessageStatusFailed,
		"error_message": reason,
	})
	if a.WSHub != nil {
		a.WSHub.BroadcastToOrg(account.OrganizationID, websocket.WSMessage{
			Type: websocket.TypeStatusUpdate,
			Payload: map[string]any{
				"message_id":    message.ID,
				"contact_id":    message.ContactID,
				"status":        models.MessageStatusFailed,
				"error_message": reason,
			},
		})
	}
}

// isUnlockTap reports whether a button reply is one of the unlock template's own
// buttons. A Yes on a reminder is a real answer, so it stays as it is.
func (a *App) isUnlockTap(account *models.WhatsAppAccount, trigger *models.Message) bool {
	if trigger == nil || trigger.MessageType != "button_reply" {
		return false
	}
	english, arabic := a.unlockTemplateNames(account.OrganizationID)
	var templates []models.Template
	a.DB.Where("organization_id = ? AND whats_app_account = ? AND name IN ?", account.OrganizationID, account.Name, []string{english, arabic}).Find(&templates)
	for i := range templates {
		for _, button := range templates[i].Buttons {
			fields, ok := button.(map[string]any)
			if ok && fields["text"] == trigger.Content {
				return true
			}
		}
	}
	return false
}

// CancelHeldMessage removes a message that hasn't gone out yet.
func (a *App) CancelHeldMessage(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}
	messageID, err := parsePathUUID(r, "message_id", "message")
	if err != nil {
		return nil
	}

	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)
	query = a.scopeAssignedContact(query, userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	result := a.DB.Where("id = ? AND contact_id = ? AND status = ?", messageID, contactID, models.MessageStatusHeld).Delete(&models.Message{})
	if result.Error != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to cancel the message", nil, "")
	}
	if result.RowsAffected == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict, "This message has already gone out", nil, "")
	}
	if a.WSHub != nil {
		a.WSHub.BroadcastToOrg(orgID, websocket.WSMessage{
			Type:    websocket.TypeStatusUpdate,
			Payload: map[string]any{"message_id": messageID, "contact_id": contactID, "status": "cancelled"},
		})
	}
	return r.SendEnvelope(map[string]any{"status": "ok"})
}
