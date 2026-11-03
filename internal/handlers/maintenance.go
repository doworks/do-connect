package handlers

import (
	"context"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
)

const (
	maintenanceInterval = 30 * time.Second
	housekeepingEvery   = 10 // Runs of the loop between the slower jobs: 5 minutes.

	// A send takes seconds. Past this, the process stopped before it could finish.
	stuckSendAfter = 15 * time.Minute

	// These rows hold patient messages, so they aren't kept longer than needed.
	deliveredDeliveryRetention = 7 * 24 * time.Hour
	failedDeliveryRetention    = 30 * 24 * time.Hour
	incomingQueueRetention     = 7 * 24 * time.Hour

	mediaRetryWindow = 7 * 24 * time.Hour
	mediaMaxAttempts = 5
)

// RunMaintenance retries undelivered events and unsaved messages until ctx ends.
func (a *App) RunMaintenance(ctx context.Context) {
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	for run := 1; ; run++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		a.ProcessDueDeliveries(ctx)
		a.ReprocessIncomingMessages(ctx)
		if run%housekeepingEvery == 0 {
			a.failStuckSends()
			a.RetryFailedMediaDownloads(ctx)
			a.cleanUpOldRows()
		}
	}
}

// failStuckSends stops messages staying on "pending" forever after a restart.
func (a *App) failStuckSends() {
	var stuck []models.Message
	if err := a.DB.Where("direction = ? AND status = ? AND updated_at < ?", models.DirectionOutgoing, models.MessageStatusPending, time.Now().Add(-stuckSendAfter)).
		Limit(100).Find(&stuck).Error; err != nil {
		return
	}
	const reason = "Not confirmed as sent: the server restarted while sending. Please check and send again."
	for i := range stuck {
		a.DB.Model(&models.Message{}).Where("id = ?", stuck[i].ID).Updates(map[string]any{
			"status":        models.MessageStatusFailed,
			"error_message": reason,
		})
		a.markContactSendFailed(&stuck[i])
		a.dispatchStatusEvent(&stuck[i], models.MessageStatusFailed, 0, reason)
	}
}

func (a *App) cleanUpOldRows() {
	now := time.Now()
	a.DB.Unscoped().Where("status = ? AND created_at < ?", models.WebhookDeliveryDelivered, now.Add(-deliveredDeliveryRetention)).Delete(&models.WebhookDelivery{})
	a.DB.Unscoped().Where("status = ? AND created_at < ?", models.WebhookDeliveryFailed, now.Add(-failedDeliveryRetention)).Delete(&models.WebhookDelivery{})
	a.DB.Unscoped().Where("processed_at IS NOT NULL AND created_at < ?", now.Add(-incomingQueueRetention)).Delete(&models.IncomingWebhook{})
}

// RetryFailedMediaDownloads fetches files that failed on arrival. Meta keeps them
// available for a while, so a short outage doesn't have to lose a patient's photo.
func (a *App) RetryFailedMediaDownloads(ctx context.Context) {
	var messages []models.Message
	if err := a.DB.Where("direction = ? AND (media_url IS NULL OR media_url = '') AND metadata ? 'media_id' AND created_at > ?", models.DirectionIncoming, time.Now().Add(-mediaRetryWindow)).
		Limit(20).Find(&messages).Error; err != nil {
		a.Log.Error("Failed to load messages missing media", "error", err)
		return
	}
	for i := range messages {
		if ctx.Err() != nil {
			return
		}
		attempts, _ := messages[i].Metadata["media_attempts"].(float64)
		if int(attempts) >= mediaMaxAttempts {
			continue
		}
		_ = a.downloadMessageMedia(ctx, &messages[i])
	}
}
