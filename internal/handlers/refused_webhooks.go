package handlers

import (
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// An event that fails the signature check is still kept. A relay in front of
// Whatomate that drops Meta's signature, or a wrong App Secret, otherwise loses
// patients' messages without anyone noticing.

const (
	refusedMissingSignature = "missing_signature"
	refusedWrongSignature   = "wrong_signature"

	// Stops a flood of fake posts filling the database. Real traffic for one
	// number is far below this.
	refusedPerHourLimit = 300
	refusedMaxBodySize  = 512 * 1024
	refusedRetention    = 30 * 24 * time.Hour
	// The warning covers this long, so an old problem doesn't stay red forever.
	refusedAlertWindow = 7 * 24 * time.Hour
)

func (a *App) saveRefusedWebhook(account *models.WhatsAppAccount, phoneNumberID, reason string, body []byte, payload *WebhookPayload) {
	if len(body) > refusedMaxBodySize {
		a.Log.Warn("Refused webhook too large to keep", "phone_id", phoneNumberID, "size", len(body))
		return
	}
	var recent int64
	a.DB.Model(&models.RefusedWebhook{}).
		Where("phone_number_id = ? AND created_at > ?", phoneNumberID, time.Now().Add(-time.Hour)).
		Count(&recent)
	if recent >= refusedPerHourLimit {
		a.Log.Warn("Too many refused webhooks this hour, not keeping more", "phone_id", phoneNumberID)
		return
	}
	messages, statuses := countPayloadEvents(payload)
	row := &models.RefusedWebhook{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: account.OrganizationID,
		AccountID:      account.ID,
		PhoneNumberID:  phoneNumberID,
		Reason:         reason,
		Body:           string(body),
		MessageCount:   messages,
		StatusCount:    statuses,
	}
	if err := a.DB.Create(row).Error; err != nil {
		a.Log.Error("Failed to keep refused webhook", "error", err, "phone_id", phoneNumberID)
	}
}

func countPayloadEvents(payload *WebhookPayload) (messages, statuses int) {
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			messages += len(change.Value.Messages)
			statuses += len(change.Value.Statuses)
		}
	}
	return messages, statuses
}

// RefusedMessagePreview lets an admin judge whether a refused event is genuine.
type RefusedMessagePreview struct {
	From string `json:"from"`
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type RefusedWebhookResponse struct {
	ID             uuid.UUID               `json:"id"`
	CreatedAt      time.Time               `json:"created_at"`
	Reason         string                  `json:"reason"`
	MessageCount   int                     `json:"message_count"`
	StatusCount    int                     `json:"status_count"`
	Messages       []RefusedMessagePreview `json:"messages,omitempty"`
	AcceptedAt     *time.Time              `json:"accepted_at,omitempty"`
	AcceptedByName string                  `json:"accepted_by_name,omitempty"`
}

func refusedMessagePreviews(body string) []RefusedMessagePreview {
	var payload WebhookPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil
	}
	var previews []RefusedMessagePreview
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				previews = append(previews, RefusedMessagePreview{From: msg.From, Type: msg.Type, Text: shortText(incomingMessageText(msg), 160)})
			}
		}
	}
	return previews
}

func incomingMessageText(msg IncomingTextMessage) string {
	switch {
	case msg.Text != nil:
		return msg.Text.Body
	case msg.Button != nil:
		return msg.Button.Text
	case msg.Interactive != nil && msg.Interactive.ButtonReply != nil:
		return msg.Interactive.ButtonReply.Title
	case msg.Interactive != nil && msg.Interactive.ListReply != nil:
		return msg.Interactive.ListReply.Title
	case msg.Image != nil:
		return msg.Image.Caption
	case msg.Video != nil:
		return msg.Video.Caption
	case msg.Document != nil:
		if msg.Document.Caption != "" {
			return msg.Document.Caption
		}
		return msg.Document.Filename
	}
	return ""
}

func shortText(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}

// ListRefusedWebhooks shows the events refused for one number, newest first.
func (a *App) ListRefusedWebhooks(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAccounts, models.ActionRead)
	if err != nil {
		return nil
	}
	accountID, err := parsePathUUID(r, "id", "account")
	if err != nil {
		return nil
	}
	if _, err := findByIDAndOrg[models.WhatsAppAccount](a.DB, r, accountID, orgID, "Account"); err != nil {
		return nil
	}

	pg := parsePagination(r)
	query := a.DB.Model(&models.RefusedWebhook{}).Where("account_id = ? AND organization_id = ?", accountID, orgID)
	var total int64
	query.Count(&total)
	var rows []models.RefusedWebhook
	if err := query.Order("created_at DESC").Offset(pg.Offset).Limit(pg.Limit).Find(&rows).Error; err != nil {
		a.Log.Error("failed to list refused webhooks", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list refused events", nil, "")
	}

	var userIDs []uuid.UUID
	for i := range rows {
		if rows[i].AcceptedByID != nil {
			userIDs = append(userIDs, *rows[i].AcceptedByID)
		}
	}
	names := map[uuid.UUID]string{}
	if len(userIDs) > 0 {
		var users []models.User
		a.DB.Select("id", "full_name").Where("id IN ?", userIDs).Find(&users)
		for i := range users {
			names[users[i].ID] = users[i].FullName
		}
	}

	response := make([]RefusedWebhookResponse, len(rows))
	for i, row := range rows {
		response[i] = RefusedWebhookResponse{
			ID:           row.ID,
			CreatedAt:    row.CreatedAt,
			Reason:       row.Reason,
			MessageCount: row.MessageCount,
			StatusCount:  row.StatusCount,
			Messages:     refusedMessagePreviews(row.Body),
			AcceptedAt:   row.AcceptedAt,
		}
		if row.AcceptedByID != nil {
			response[i].AcceptedByName = names[*row.AcceptedByID]
		}
	}
	return r.SendEnvelope(listEnvelope("events", response, total, pg))
}

// AcceptRefusedWebhook processes a refused event as if it had passed the check.
// Messages already saved are skipped, so accepting twice does no harm.
func (a *App) AcceptRefusedWebhook(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAccounts, models.ActionWrite)
	if err != nil {
		return nil
	}
	accountID, err := parsePathUUID(r, "id", "account")
	if err != nil {
		return nil
	}
	eventID, err := parsePathUUID(r, "event_id", "event")
	if err != nil {
		return nil
	}

	var row models.RefusedWebhook
	if err := a.DB.Where("id = ? AND account_id = ? AND organization_id = ?", eventID, accountID, orgID).First(&row).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Refused event not found", nil, "")
	}
	if row.AcceptedAt != nil {
		return r.SendEnvelope(map[string]any{"accepted_at": row.AcceptedAt})
	}

	var payload WebhookPayload
	if err := json.Unmarshal([]byte(row.Body), &payload); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, "This event can't be read", nil, "")
	}
	// Only the number this event was refused for, so accepting can't slip in
	// events for another organisation's number.
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if id := change.Value.Metadata.PhoneNumberID; id != "" && id != row.PhoneNumberID {
				return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, "This event mixes several numbers and can't be accepted", nil, "")
			}
		}
	}
	if err := a.processWebhookPayload(&payload, true); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Could not save the message", nil, "")
	}

	now := time.Now()
	a.DB.Model(&models.RefusedWebhook{}).Where("id = ?", row.ID).
		Updates(map[string]any{"accepted_at": now, "accepted_by_id": userID})
	return r.SendEnvelope(map[string]any{"accepted_at": now})
}

// RefusedWebhookSummary feeds the warning shown to admins: how many events were
// refused recently and not accepted, per number.
func (a *App) RefusedWebhookSummary(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAccounts, models.ActionRead)
	if err != nil {
		return nil
	}
	type accountCount struct {
		AccountID    uuid.UUID `json:"account_id"`
		Events       int       `json:"events"`
		Messages     int       `json:"messages"`
		LatestAt     time.Time `json:"latest_at"`
		LatestReason string    `json:"latest_reason"`
	}
	var counts []accountCount
	a.DB.Model(&models.RefusedWebhook{}).
		Select("account_id, count(*) AS events, coalesce(sum(message_count), 0) AS messages, max(created_at) AS latest_at, "+
			"(array_agg(reason ORDER BY created_at DESC))[1] AS latest_reason").
		Where("organization_id = ? AND accepted_at IS NULL AND created_at > ?", orgID, time.Now().Add(-refusedAlertWindow)).
		Group("account_id").Scan(&counts)
	if counts == nil {
		counts = []accountCount{}
	}
	return r.SendEnvelope(map[string]any{"accounts": counts})
}
