package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// contactUpdates collects the contact_update messages that reach a client.
func contactUpdates(client *websocket.Client) []map[string]any {
	var out []map[string]any
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case raw := <-client.SendChan():
			var message struct {
				Type    string         `json:"type"`
				Payload map[string]any `json:"payload"`
			}
			if json.Unmarshal(raw, &message) == nil && message.Type == websocket.TypeContactUpdate {
				out = append(out, message.Payload)
			}
		case <-deadline:
			return out
		}
	}
}

func TestContactRenameReachesOpenScreens(t *testing.T) {
	f := newHeldFixture(t)
	hub := websocket.NewHub(f.app.Log)
	go hub.Run()
	f.app.WSHub = hub
	client := websocket.NewClient(hub, nil, f.user.ID, f.org.ID)
	hub.Register(client)
	testutil.AssertEventually(t, func() bool { return hub.IsUserOnline(f.org.ID, f.user.ID) }, 2*time.Second, "user online")

	contact := f.contact(t, func(c *models.Contact) { c.ProfileName = "Abu Omar"; c.WhatsAppName = "Abu Omar" })

	t.Run("an integration naming the chat after the patient tells every open inbox", func(t *testing.T) {
		req := testutil.NewJSONRequest(t, map[string]any{
			"profile_name": "EN · Noor Khalid Saleh",
			"tags":         []string{"Patient", "English"},
		})
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		testutil.SetPathParam(req, "id", contact.ID.String())
		require.NoError(t, f.app.UpdateContact(req))
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

		updates := contactUpdates(client)
		require.Len(t, updates, 1)
		assert.Equal(t, contact.ID.String(), updates[0]["contact_id"])
		assert.Equal(t, "EN · Noor Khalid Saleh", updates[0]["name"])
		assert.Equal(t, []any{"Patient", "English"}, updates[0]["tags"])

		var saved models.Contact
		require.NoError(t, f.app.DB.First(&saved, contact.ID).Error)
		assert.True(t, saved.NameLocked, "the next message from them doesn't undo it")
	})

	t.Run("changing only the tags tells them too", func(t *testing.T) {
		req := testutil.NewJSONRequest(t, map[string]any{"tags": []string{"VIP"}})
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		testutil.SetPathParam(req, "id", contact.ID.String())
		require.NoError(t, f.app.UpdateContactTags(req))

		updates := contactUpdates(client)
		require.Len(t, updates, 1)
		assert.Equal(t, []any{"VIP"}, updates[0]["tags"])
		assert.Equal(t, "EN · Noor Khalid Saleh", updates[0]["name"])
	})

	t.Run("assigning the chat isn't a rename", func(t *testing.T) {
		req := testutil.NewJSONRequest(t, map[string]any{"assigned_user_id": f.user.ID})
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		testutil.SetPathParam(req, "id", contact.ID.String())
		require.NoError(t, f.app.UpdateContact(req))
		assert.Empty(t, contactUpdates(client))
	})
}

func TestFirstPushWaitsForTheChatName(t *testing.T) {
	// A clinic system that names chats from the message.incoming webhook.
	namedByIntegration := func(t *testing.T, f pushFixture) {
		t.Helper()
		require.NoError(t, f.app.DB.Create(&models.Webhook{
			OrganizationID: f.org.ID,
			Name:           "Clinic",
			URL:            "https://clinic.invalid/hook",
			Events:         models.StringArray{string(models.WebhookEventMessageIncoming)},
			IsActive:       true,
		}).Error)
	}
	// What the clinic system does a moment after the message arrives.
	renameSoon := func(f pushFixture, contact *models.Contact, name string) {
		go func() {
			time.Sleep(500 * time.Millisecond)
			f.app.DB.Model(&models.Contact{}).Where("id = ?", contact.ID).
				Updates(map[string]any{"profile_name": name, "name_locked": true})
		}()
	}
	title := func(f pushFixture) string {
		f.fcm.mu.Lock()
		defer f.fcm.mu.Unlock()
		require.Len(t, f.fcm.messages, 1)
		return f.fcm.messages[0]["notification"].(map[string]any)["title"].(string)
	}

	t.Run("a new chat's first notification carries the patient name", func(t *testing.T) {
		f := newPushFixture(t)
		namedByIntegration(t, f)
		f.phone(t, f.user, "token-admin")
		contact := f.contact(t, func(c *models.Contact) { c.ProfileName = "👑 Noura"; c.WhatsAppName = "👑 Noura" })

		renameSoon(f, contact, "EN · Layla Hassan Saleh")
		f.arrives(t, contact, "text", "Hello")
		assert.Equal(t, "EN · Layla Hassan Saleh", title(f))
	})

	t.Run("a new number nobody knows still notifies, with its WhatsApp name", func(t *testing.T) {
		f := newPushFixture(t)
		namedByIntegration(t, f)
		f.phone(t, f.user, "token-admin")
		contact := f.contact(t, func(c *models.Contact) { c.ProfileName = "Stranger"; c.WhatsAppName = "Stranger" })

		start := time.Now()
		f.arrives(t, contact, "text", "Hi, how much is whitening?")
		assert.Equal(t, "Stranger", title(f))
		assert.Less(t, time.Since(start), pushNameWait+2*time.Second, "waits a few seconds at most")
	})

	t.Run("a chat that already has its name doesn't wait", func(t *testing.T) {
		f := newPushFixture(t)
		namedByIntegration(t, f)
		f.phone(t, f.user, "token-admin")
		contact := f.contact(t, func(c *models.Contact) { c.ProfileName = "EN · Omar Faisal +1"; c.NameLocked = true })

		start := time.Now()
		f.arrives(t, contact, "text", "Thanks")
		assert.Equal(t, "EN · Omar Faisal +1", title(f))
		assert.Less(t, time.Since(start), time.Second)
	})

	t.Run("without anything to name chats, a new chat doesn't wait", func(t *testing.T) {
		f := newPushFixture(t)
		f.phone(t, f.user, "token-admin")
		contact := f.contact(t, func(c *models.Contact) { c.ProfileName = "Stranger" })

		start := time.Now()
		f.arrives(t, contact, "text", "Hi")
		assert.Equal(t, "Stranger", title(f))
		assert.Less(t, time.Since(start), time.Second)
	})
}
