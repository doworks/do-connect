package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Each delivery is saved before it's attempted, so an integration that's down
// for a while still gets the event once it's back.

// A brief blip shouldn't delay a patient's answer, so the first tries are seconds
// apart. Then deliveryBackoff takes over.
const quickDeliveryTries = 3

// deliveryBackoff is the wait before each later retry: about 16 hours in total.
var deliveryBackoff = []time.Duration{
	time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour,
}

const (
	webhookAttemptTimeout = 15 * time.Second
	// A delivery being attempted is left alone for this long, in case the
	// process stops halfway through.
	deliveryLease = 10 * time.Minute
)

// attemptDelivery makes one try and records the outcome. The secret, URL and
// headers come from the database each time, so a changed secret applies to
// retries too.
func (a *App) attemptDelivery(ctx context.Context, delivery *models.WebhookDelivery) error {
	var webhook models.Webhook
	if err := a.DB.Where("id = ?", delivery.WebhookID).First(&webhook).Error; err != nil {
		a.finishDelivery(delivery, models.WebhookDeliveryFailed, 0, "The webhook no longer exists")
		return err
	}

	attemptCtx, cancel := context.WithTimeout(ctx, webhookAttemptTimeout)
	defer cancel()
	err := a.sendWebhookRequest(attemptCtx, webhook, []byte(delivery.Payload))
	delivery.Attempts++

	if err == nil {
		a.finishDelivery(delivery, models.WebhookDeliveryDelivered, 200, "")
		return nil
	}

	statusCode := 0
	var webhookErr *WebhookError
	if errors.As(err, &webhookErr) {
		statusCode = webhookErr.StatusCode
	}
	if delivery.Attempts >= quickDeliveryTries+len(deliveryBackoff) {
		a.finishDelivery(delivery, models.WebhookDeliveryFailed, statusCode, err.Error())
		a.Log.Error("webhook delivery gave up", "webhook_id", webhook.ID, "event", delivery.Event, "attempts", delivery.Attempts, "error", err)
		return err
	}
	wait := deliveryBackoff[0]
	if delivery.Attempts > quickDeliveryTries {
		wait = deliveryBackoff[delivery.Attempts-quickDeliveryTries-1]
	}
	next := time.Now().Add(wait)
	delivery.NextAttemptAt = &next
	a.DB.Model(&models.WebhookDelivery{}).Where("id = ?", delivery.ID).Updates(map[string]any{
		"attempts":         delivery.Attempts,
		"last_error":       err.Error(),
		"last_status_code": statusCode,
		"next_attempt_at":  next,
	})
	a.Log.Warn("webhook delivery failed, will retry", "webhook_id", webhook.ID, "event", delivery.Event, "attempt", delivery.Attempts, "next_attempt_at", next, "error", err)
	return err
}

func (a *App) finishDelivery(delivery *models.WebhookDelivery, status models.WebhookDeliveryStatus, statusCode int, lastError string) {
	updates := map[string]any{
		"status":           status,
		"attempts":         delivery.Attempts,
		"last_error":       lastError,
		"last_status_code": statusCode,
		"next_attempt_at":  nil,
	}
	delivery.Status = status
	delivery.LastError = lastError
	delivery.LastStatusCode = statusCode
	delivery.NextAttemptAt = nil
	if status == models.WebhookDeliveryDelivered {
		now := time.Now()
		updates["delivered_at"] = now
		delivery.DeliveredAt = &now
	}
	a.DB.Model(&models.WebhookDelivery{}).Where("id = ?", delivery.ID).Updates(updates)
}

// ProcessDueDeliveries retries what's due. Several server instances can run this
// at once: each row goes to one of them.
func (a *App) ProcessDueDeliveries(ctx context.Context) int {
	var due []models.WebhookDelivery
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND next_attempt_at <= ?", models.WebhookDeliveryPending, time.Now()).
			Order("next_attempt_at ASC").Limit(50).Find(&due).Error; err != nil {
			return err
		}
		if len(due) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(due))
		for i := range due {
			ids[i] = due[i].ID
		}
		return tx.Model(&models.WebhookDelivery{}).Where("id IN ?", ids).
			Update("next_attempt_at", time.Now().Add(deliveryLease)).Error
	})
	if err != nil {
		a.Log.Error("failed to load due webhook deliveries", "error", err)
		return 0
	}
	for i := range due {
		if ctx.Err() != nil {
			break
		}
		_ = a.attemptDelivery(ctx, &due[i])
	}
	return len(due)
}

// WebhookDeliveryResponse leaves out the payload, which holds patient messages.
type WebhookDeliveryResponse struct {
	ID             uuid.UUID                    `json:"id"`
	Event          string                       `json:"event"`
	Status         models.WebhookDeliveryStatus `json:"status"`
	Attempts       int                          `json:"attempts"`
	LastError      string                       `json:"last_error,omitempty"`
	LastStatusCode int                          `json:"last_status_code,omitempty"`
	NextAttemptAt  *time.Time                   `json:"next_attempt_at,omitempty"`
	DeliveredAt    *time.Time                   `json:"delivered_at,omitempty"`
	CreatedAt      time.Time                    `json:"created_at"`
}

func deliveryResponse(delivery *models.WebhookDelivery) WebhookDeliveryResponse {
	return WebhookDeliveryResponse{
		ID:             delivery.ID,
		Event:          delivery.Event,
		Status:         delivery.Status,
		Attempts:       delivery.Attempts,
		LastError:      delivery.LastError,
		LastStatusCode: delivery.LastStatusCode,
		NextAttemptAt:  delivery.NextAttemptAt,
		DeliveredAt:    delivery.DeliveredAt,
		CreatedAt:      delivery.CreatedAt,
	}
}

// ListWebhookDeliveries shows recent deliveries for one webhook.
func (a *App) ListWebhookDeliveries(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if !a.HasPermission(userID, models.ResourceWebhooks, models.ActionRead, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Insufficient permissions", nil, "")
	}
	webhookID, err := parsePathUUID(r, "id", "webhook")
	if err != nil {
		return nil
	}
	if _, err := findByIDAndOrg[models.Webhook](a.DB, r, webhookID, orgID, "Webhook"); err != nil {
		return nil
	}

	pg := parsePagination(r)
	query := a.DB.Model(&models.WebhookDelivery{}).Where("webhook_id = ? AND organization_id = ?", webhookID, orgID)
	if status := string(r.RequestCtx.QueryArgs().Peek("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	query.Count(&total)
	var rows []models.WebhookDelivery
	if err := query.Order("created_at DESC").Offset(pg.Offset).Limit(pg.Limit).Find(&rows).Error; err != nil {
		a.Log.Error("failed to list webhook deliveries", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list deliveries", nil, "")
	}
	response := make([]WebhookDeliveryResponse, len(rows))
	for i := range rows {
		response[i] = deliveryResponse(&rows[i])
	}
	return r.SendEnvelope(listEnvelope("deliveries", response, total, pg))
}

// ResendWebhookDelivery tries one delivery again now and reports the result.
func (a *App) ResendWebhookDelivery(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if !a.HasPermission(userID, models.ResourceWebhooks, models.ActionWrite, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Insufficient permissions", nil, "")
	}
	webhookID, err := parsePathUUID(r, "id", "webhook")
	if err != nil {
		return nil
	}
	deliveryID, err := parsePathUUID(r, "delivery_id", "delivery")
	if err != nil {
		return nil
	}
	var delivery models.WebhookDelivery
	if err := a.DB.Where("id = ? AND webhook_id = ? AND organization_id = ?", deliveryID, webhookID, orgID).First(&delivery).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Delivery not found", nil, "")
	}

	// A manual resend gets a fresh set of retries.
	delivery.Attempts = 0
	delivery.Status = models.WebhookDeliveryPending
	lease := time.Now().Add(deliveryLease)
	delivery.NextAttemptAt = &lease
	a.DB.Model(&models.WebhookDelivery{}).Where("id = ?", delivery.ID).Updates(map[string]any{
		"status": delivery.Status, "attempts": 0, "next_attempt_at": lease,
	})
	if err := a.attemptDelivery(context.Background(), &delivery); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadGateway, fmt.Sprintf("Still failing: %s", err.Error()), nil, "")
	}
	return r.SendEnvelope(deliveryResponse(&delivery))
}

// newDelivery saves a delivery so it survives a restart or an outage.
func (a *App) newDelivery(webhook models.Webhook, eventType string, payload []byte) (*models.WebhookDelivery, error) {
	lease := time.Now().Add(deliveryLease)
	delivery := &models.WebhookDelivery{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: webhook.OrganizationID,
		WebhookID:      webhook.ID,
		Event:          eventType,
		Payload:        string(payload),
		Status:         models.WebhookDeliveryPending,
		NextAttemptAt:  &lease,
	}
	return delivery, a.DB.Create(delivery).Error
}

// dispatchStatusEvent tells integrations how an outgoing message went, including why it failed.
func (a *App) dispatchStatusEvent(message *models.Message, status models.MessageStatus, errorCode int, errorText string) {
	if message.Direction != models.DirectionOutgoing {
		return
	}
	var contact models.Contact
	if err := a.DB.Select("id", "phone_number").Where("id = ?", message.ContactID).First(&contact).Error; err != nil {
		return
	}
	a.DispatchWebhook(message.OrganizationID, models.WebhookEventMessageStatus, MessageStatusEventData{
		MessageID:         message.ID.String(),
		WhatsAppMessageID: message.WhatsAppMessageID,
		ContactID:         contact.ID.String(),
		ContactPhone:      contact.PhoneNumber,
		WhatsAppAccount:   message.WhatsAppAccount,
		TemplateName:      message.TemplateName,
		Status:            status,
		ErrorCode:         errorCode,
		ErrorMessage:      errorText,
	})
}

// MessageStatusEventData is sent when an outgoing message is delivered, read or fails.
type MessageStatusEventData struct {
	MessageID         string               `json:"message_id"`
	WhatsAppMessageID string               `json:"whatsapp_message_id,omitempty"`
	ContactID         string               `json:"contact_id"`
	ContactPhone      string               `json:"contact_phone"`
	WhatsAppAccount   string               `json:"whatsapp_account"`
	TemplateName      string               `json:"template_name,omitempty"`
	Status            models.MessageStatus `json:"status"`
	ErrorCode         int                  `json:"error_code,omitempty"`
	ErrorMessage      string               `json:"error_message,omitempty"`
}
