package handlers

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageSentAt(t *testing.T) {
	received := time.Date(2026, 3, 2, 12, 3, 0, 250_000_000, time.UTC)
	sent := time.Date(2026, 3, 2, 8, 2, 0, 0, time.UTC)

	at := messageSentAt(strconv.FormatInt(sent.Unix(), 10), received)
	assert.Equal(t, sent.Unix(), at.Unix(), "the patient's time, not ours")
	assert.Equal(t, 250_000_000, at.Nanosecond(), "the arrival keeps messages from one second in order")

	for name, timestamp := range map[string]string{
		"missing":       "",
		"not a number":  "soon",
		"zero":          "0",
		"in the future": strconv.FormatInt(received.Add(10*time.Minute).Unix(), 10),
	} {
		assert.Equal(t, received, messageSentAt(timestamp, received), name)
	}
	// A clock a little ahead of ours can't put a message after its arrival.
	ahead := messageSentAt(strconv.FormatInt(received.Add(time.Minute).Unix(), 10), received)
	assert.Equal(t, received, ahead)
}

func TestLateIncomingMessages(t *testing.T) {
	saved := func(t *testing.T, f heldFixture, wamid string) models.Message {
		t.Helper()
		var message models.Message
		require.NoError(t, f.app.DB.Where("whats_app_message_id = ?", wamid).First(&message).Error)
		return message
	}
	reload := func(t *testing.T, f heldFixture, contact *models.Contact) models.Contact {
		t.Helper()
		var c models.Contact
		require.NoError(t, f.app.DB.First(&c, contact.ID).Error)
		return c
	}
	arrives := func(f heldFixture, contact *models.Contact, text string, sentAt time.Time) string {
		wamid := "wamid." + uuid.NewString()
		own := *contact
		f.app.saveIncomingMessageAt(f.account, &own, wamid, "text", text, nil, "", sentAt)
		f.app.WaitForBackgroundTasks()
		return wamid
	}

	t.Run("a late message shows when it was sent and when it arrived", func(t *testing.T) {
		f := newHeldFixture(t)
		contact := f.contact(t)
		sentAt := time.Now().Add(-4 * time.Hour)

		message := saved(t, f, arrives(f, contact, "Is the clinic open today?", sentAt))
		assert.WithinDuration(t, sentAt, message.CreatedAt, time.Millisecond)

		var response MessageResponse
		applySenderFields(&response, &message)
		require.NotNil(t, response.ReceivedAt)
		assert.WithinDuration(t, time.Now(), *response.ReceivedAt, time.Minute)
	})

	t.Run("one that arrives on time has just the one time", func(t *testing.T) {
		f := newHeldFixture(t)
		message := saved(t, f, arrives(f, f.contact(t), "Hello", time.Now().Add(-5*time.Second)))
		var response MessageResponse
		applySenderFields(&response, &message)
		assert.Nil(t, response.ReceivedAt)
	})

	t.Run("it doesn't move the chat back in time", func(t *testing.T) {
		f := newHeldFixture(t)
		hourAgo := time.Now().Add(-time.Hour)
		contact := f.contact(t, func(c *models.Contact) {
			c.LastInboundAt = &hourAgo
			c.LastMessageAt = &hourAgo
			c.LastMessagePreview = "See you at four"
			c.LastDirection = models.DirectionOutgoing
			c.IsRead = true
		})

		arrives(f, contact, "Running late", time.Now().Add(-3*time.Hour))

		after := reload(t, f, contact)
		assert.WithinDuration(t, hourAgo, *after.LastInboundAt, time.Millisecond)
		assert.WithinDuration(t, hourAgo, *after.LastMessageAt, time.Millisecond)
		assert.Equal(t, "See you at four", after.LastMessagePreview, "the newer message stays the preview")
		assert.Equal(t, models.DirectionOutgoing, after.LastDirection)
		assert.False(t, after.IsRead, "staff still haven't seen it")
		require.NotNil(t, after.WaitingSince)
		assert.WithinDuration(t, time.Now(), *after.WaitingSince, time.Minute, "the wait starts when staff could see it")
	})

	t.Run("the newest message still moves the chat forward", func(t *testing.T) {
		f := newHeldFixture(t)
		dayAgo := time.Now().Add(-30 * time.Hour)
		contact := f.contact(t, func(c *models.Contact) { c.LastInboundAt, c.LastMessageAt = &dayAgo, &dayAgo })
		sentAt := time.Now().Add(-3 * time.Hour)

		arrives(f, contact, "Running late", sentAt)

		after := reload(t, f, contact)
		assert.WithinDuration(t, sentAt, *after.LastInboundAt, time.Millisecond)
		assert.WithinDuration(t, sentAt, *after.LastMessageAt, time.Millisecond)
		assert.Equal(t, "Running late", after.LastMessagePreview)
	})

	t.Run("it sits where it belongs in the chat", func(t *testing.T) {
		f := newHeldFixture(t)
		contact := f.contact(t, recentlyWrote)
		reply := f.send(t, contact, "We open at nine")
		require.NoError(t, f.app.DB.Model(&models.Message{}).Where("id = ?", reply.ID).Update("created_at", time.Now().Add(-2*time.Hour)).Error)

		late := saved(t, f, arrives(f, contact, "What time do you open?", time.Now().Add(-3*time.Hour)))

		var order []uuid.UUID
		require.NoError(t, f.app.DB.Model(&models.Message{}).Where("contact_id = ?", contact.ID).Order("created_at ASC").Pluck("id", &order).Error)
		assert.Equal(t, []uuid.UUID{late.ID, reply.ID}, order)
	})

	t.Run("the 24 hours count from when they sent it", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		held := f.send(t, contact, "Your results are ready")

		arrives(f, contact, "Hello", time.Now().Add(-25*time.Hour))

		assert.Empty(t, f.meta.ofType("text"), "WhatsApp would refuse it, so it keeps waiting")
		assert.Equal(t, models.MessageStatusHeld, f.status(t, held.ID).Status)
		after := reload(t, f, contact)
		assert.False(t, serviceWindowOpen(&after))

		arrives(f, contact, "Hello again", time.Now().Add(-3*time.Hour))
		assert.Len(t, f.meta.ofType("text"), 1, "a late message still inside the window releases it")
		assert.Equal(t, models.MessageStatusSent, f.status(t, held.ID).Status)
	})

	t.Run("Meta's time is used for a message from the webhook", func(t *testing.T) {
		f := newHeldFixture(t)
		if f.app.Redis == nil {
			t.Skip("TEST_REDIS_URL not set")
		}
		contact := f.contact(t)
		sentAt := time.Now().Add(-6 * time.Hour).Truncate(time.Second)
		wamid := "wamid." + uuid.NewString()
		msg := IncomingTextMessage{From: contact.PhoneNumber, ID: wamid, Timestamp: strconv.FormatInt(sentAt.Unix(), 10), Type: "text"}
		msg.Text = &struct {
			Body string `json:"body"`
		}{Body: "Sent this morning"}

		f.app.processIncomingMessageFull(f.account.PhoneID, msg, "Patient")
		f.app.WaitForBackgroundTasks()

		message := saved(t, f, wamid)
		assert.Equal(t, sentAt.Unix(), message.CreatedAt.Unix())
		assert.NotNil(t, receivedAtFrom(message.Metadata))
	})
}
