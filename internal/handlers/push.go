package handlers

import (
	"context"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/push"
)

const (
	// pushChannelID is the Android notification channel the mobile app creates
	// for new messages.
	pushChannelID = "messages"
	// maxPushDevices bounds one message's fan-out to an unassigned chat.
	maxPushDevices  = 200
	pushTimeout     = 20 * time.Second
	pushSendTimeout = 5 * time.Second
	pushParallel    = 8
	pushPreviewMax  = 100
	// A new chat that an integration names (a clinic system naming it after
	// the patient on its message.incoming webhook) gets its name a moment
	// after its first message. Its first notification waits this long for it.
	pushNameWait = 3 * time.Second
	pushNamePoll = 200 * time.Millisecond
	// How recently a chat must have been created to count as new.
	pushNewChat = time.Minute
)

type pushDevice struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	PushToken string
}

// pushIncomingMessage notifies the phones of the staff who should answer a new
// incoming message: the chat's assigned user, or, when nobody is assigned,
// everyone in the organization who can see unassigned chats. It runs in the
// background and never delays webhook processing. Messages the chatbot
// handles arrive read and don't notify anyone.
func (a *App) pushIncomingMessage(orgID uuid.UUID, msg *models.Message, contact *models.Contact) {
	if a.Push == nil || msg == nil || contact == nil {
		return
	}
	if msg.Direction != models.DirectionIncoming || msg.MessageType == models.MessageTypeReaction {
		return
	}
	if msg.Status == models.MessageStatusRead {
		return
	}

	var assigned *uuid.UUID
	if contact.AssignedUserID != nil {
		id := *contact.AssignedUserID
		assigned = &id
	}
	name, phone := contact.ProfileName, contact.PhoneNumber
	contactID, messageID := contact.ID, msg.ID
	msgType, content := string(msg.MessageType), msg.Content
	awaitName := !contact.NameLocked && time.Since(contact.CreatedAt) < pushNewChat && a.integrationNamesChats(orgID)

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()

		devices, err := a.pushRecipients(orgID, assigned)
		if err != nil {
			a.Log.Error("Failed to find phones to notify", "error", err, "contact_id", contactID)
			return
		}
		if len(devices) == 0 {
			return
		}
		if awaitName {
			name = a.waitForChatName(ctx, contactID, name)
		}

		title := a.pushTitle(orgID, name, phone)
		body := pushPreview(msgType, content)
		data := map[string]string{"contact_id": contactID.String(), "message_id": messageID.String()}

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			sent    int
			dropped int
			failed  int
		)
		slots := make(chan struct{}, pushParallel)
		for _, device := range devices {
			wg.Add(1)
			slots <- struct{}{}
			go func(device pushDevice) {
				defer wg.Done()
				defer func() { <-slots }()
				sendCtx, cancelSend := context.WithTimeout(ctx, pushSendTimeout)
				defer cancelSend()
				err := a.Push.Send(sendCtx, push.Message{
					Token:     device.PushToken,
					Title:     title,
					Body:      body,
					Tag:       contactID.String(),
					ChannelID: pushChannelID,
					Data:      data,
				})
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					sent++
				case errors.Is(err, push.ErrInvalidToken):
					dropped++
					a.dropPushToken(device)
				default:
					failed++
					a.Log.Warn("Push not delivered", "error", err, "device_id", device.ID, "contact_id", contactID)
				}
			}(device)
		}
		wg.Wait()
		a.Log.Info("Sent new-message push", "contact_id", contactID, "message_id", messageID,
			"sent", sent, "dropped_tokens", dropped, "failed", failed)
	}()
}

// integrationNamesChats reports whether something outside may name new chats:
// an active webhook that hears about incoming messages.
func (a *App) integrationNamesChats(orgID uuid.UUID) bool {
	webhooks, err := a.getWebhooksCached(orgID)
	if err != nil {
		return false
	}
	for _, webhook := range webhooks {
		if webhook.IsActive && containsEvent(webhook.Events, string(models.WebhookEventMessageIncoming)) {
			return true
		}
	}
	return false
}

// waitForChatName gives an integration a moment to name a new chat, and
// returns the name it set, or the name the chat already had.
func (a *App) waitForChatName(ctx context.Context, contactID uuid.UUID, name string) string {
	deadline := time.NewTimer(pushNameWait)
	defer deadline.Stop()
	tick := time.NewTicker(pushNamePoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return name
		case <-deadline.C:
			return name
		case <-tick.C:
			var row struct {
				ProfileName string
				NameLocked  bool
			}
			if err := a.DB.Model(&models.Contact{}).Select("profile_name, name_locked").
				Where("id = ?", contactID).Scan(&row).Error; err == nil && row.NameLocked {
				return row.ProfileName
			}
		}
	}
}

// pushRecipients lists the phones to notify. Only signed-in phones with a push
// token, of active users who can see chats, in this organization.
func (a *App) pushRecipients(orgID uuid.UUID, assigned *uuid.UUID) ([]pushDevice, error) {
	query := a.DB.Table("mobile_devices AS d").
		Select("d.id, d.user_id, d.push_token").
		Joins("JOIN users u ON u.id = d.user_id AND u.is_active = ? AND u.deleted_at IS NULL", true).
		Where("d.organization_id = ? AND d.revoked_at IS NULL AND d.push_token <> ''", orgID).
		Order("d.last_seen_at DESC").
		Limit(maxPushDevices)
	if assigned != nil {
		query = query.Where("d.user_id = ?", *assigned)
	}
	var rows []pushDevice
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}

	// The same checks the inbox applies: chat access, and for a chat nobody
	// owns, permission to see every contact.
	allowed := map[uuid.UUID]bool{}
	devices := rows[:0]
	for _, row := range rows {
		ok, seen := allowed[row.UserID]
		if !seen {
			ok = a.HasPermission(row.UserID, models.ResourceChat, models.ActionRead, orgID) &&
				(assigned != nil || a.HasPermission(row.UserID, models.ResourceContacts, models.ActionRead, orgID))
			allowed[row.UserID] = ok
		}
		if ok {
			devices = append(devices, row)
		}
	}
	return devices, nil
}

// dropPushToken forgets a token FCM no longer accepts, unless the phone has
// sent a new one meanwhile.
func (a *App) dropPushToken(device pushDevice) {
	if err := a.DB.Model(&models.MobileDevice{}).
		Where("id = ? AND push_token = ?", device.ID, device.PushToken).
		Update("push_token", "").Error; err != nil {
		a.Log.Error("Failed to clear push token", "error", err, "device_id", device.ID)
		return
	}
	a.Log.Info("Cleared push token FCM no longer accepts", "device_id", device.ID)
}

// pushTitle is the contact's name, or their number, masked like everywhere
// else when the organization hides phone numbers.
func (a *App) pushTitle(orgID uuid.UUID, name, phone string) string {
	name, phone = a.MaskContactFields(orgID, name, phone)
	if name != "" {
		return name
	}
	if phone != "" && phone[0] != '+' && phone[0] != '*' {
		phone = "+" + phone
	}
	if phone == "" {
		return "New message"
	}
	return phone
}

// pushPreview is a short line for the notification: the text, or what kind
// of attachment came in.
func pushPreview(msgType, content string) string {
	switch msgType {
	case "text", "button_reply":
		if content == "" {
			return "New message"
		}
		if utf8.RuneCountInString(content) > pushPreviewMax {
			runes := []rune(content)
			return string(runes[:pushPreviewMax-1]) + "…"
		}
		return content
	case "image":
		return "Photo"
	case "video":
		return "Video"
	case "audio":
		return "Voice message"
	case "document":
		return "Document"
	case "sticker":
		return "Sticker"
	case "location":
		return "Location"
	case "contacts", "contact":
		return "Contact card"
	case "nfm_reply":
		return "Form reply"
	default:
		return "New message"
	}
}
