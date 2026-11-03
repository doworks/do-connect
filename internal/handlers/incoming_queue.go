package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// A patient's message is saved here before Meta gets its 200. Meta won't resend
// after a 200, so a crash or database error while processing would otherwise
// lose the message.

const (
	incomingRetryAfter  = 5 * time.Minute
	incomingMaxAttempts = 5
	// Only stops two copies being processed at once. After this, the check
	// against saved messages takes over, which lets a failed attempt run again.
	duplicateClaimTTL = 2 * time.Minute
)

// messageTypesThatSave are the types that end up as a row in messages, which is
// how a retry knows whether the first attempt worked.
var messageTypesThatSave = map[string]bool{
	"text": true, "image": true, "video": true, "audio": true, "document": true,
	"sticker": true, "button": true, "interactive": true, "location": true, "contacts": true,
}

func (a *App) queueIncomingMessage(phoneNumberID string, msg IncomingTextMessage, profileName string) (*models.IncomingWebhook, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	row := &models.IncomingWebhook{
		BaseModel:         models.BaseModel{ID: uuid.New()},
		PhoneNumberID:     phoneNumberID,
		WhatsAppMessageID: msg.ID,
		ProfileName:       profileName,
		Body:              string(body),
	}
	return row, a.DB.Create(row).Error
}

// processQueuedMessage runs one attempt and closes the row only if the message
// was really saved.
func (a *App) processQueuedMessage(row *models.IncomingWebhook, msg IncomingTextMessage) {
	a.processIncomingMessage(row.PhoneNumberID, msg, row.ProfileName)
	row.Attempts++

	saved := true
	if messageTypesThatSave[msg.Type] && msg.ID != "" {
		var count int64
		a.DB.Model(&models.Message{}).Where("whats_app_message_id = ?", msg.ID).Count(&count)
		saved = count > 0
	}
	updates := map[string]any{"attempts": row.Attempts}
	switch {
	case saved:
		updates["processed_at"] = time.Now()
	case row.Attempts >= incomingMaxAttempts:
		updates["processed_at"] = time.Now()
		a.Log.Error("Incoming message could not be saved, giving up", "message_id", msg.ID, "attempts", row.Attempts)
	}
	a.DB.Model(&models.IncomingWebhook{}).Where("id = ?", row.ID).Updates(updates)
}

// ReprocessIncomingMessages picks up messages whose first attempt never finished.
func (a *App) ReprocessIncomingMessages(ctx context.Context) int {
	var rows []models.IncomingWebhook
	if err := a.DB.Where("processed_at IS NULL AND updated_at < ?", time.Now().Add(-incomingRetryAfter)).
		Order("created_at ASC").Limit(50).Find(&rows).Error; err != nil {
		a.Log.Error("Failed to load unprocessed incoming messages", "error", err)
		return 0
	}
	for i := range rows {
		if ctx.Err() != nil {
			break
		}
		var msg IncomingTextMessage
		if err := json.Unmarshal([]byte(rows[i].Body), &msg); err != nil {
			a.Log.Error("Unreadable queued message", "error", err, "row_id", rows[i].ID)
			a.DB.Model(&models.IncomingWebhook{}).Where("id = ?", rows[i].ID).Update("processed_at", time.Now())
			continue
		}
		a.processQueuedMessage(&rows[i], msg)
	}
	return len(rows)
}

// claimIncomingMessage returns false if another copy of this message is being
// processed right now. Meta can deliver the same event twice at the same moment.
func (a *App) claimIncomingMessage(messageID string) bool {
	if a.Redis == nil || messageID == "" {
		return true
	}
	claimed, err := a.Redis.SetNX(context.Background(), fmt.Sprintf("incoming:claim:%s", messageID), "1", duplicateClaimTTL).Result()
	if err != nil {
		return true // The saved-message check still catches most duplicates.
	}
	return claimed
}
