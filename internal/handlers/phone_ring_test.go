package handlers

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

var callTaker = []string{"chat:read", "contacts:read", "call_transfers:read", "call_transfers:write"}

func (f pushFixture) sentTypes() map[string][]string {
	f.fcm.mu.Lock()
	defer f.fcm.mu.Unlock()
	out := map[string][]string{}
	for _, m := range f.fcm.messages {
		data, _ := m["data"].(map[string]any)
		kind, _ := data["type"].(string)
		out[kind] = append(out[kind], m["token"].(string))
	}
	return out
}

func (f pushFixture) offer(contact *models.Contact) map[string]any {
	return map[string]any{
		"id":               uuid.NewString(),
		"call_log_id":      uuid.NewString(),
		"contact_id":       contact.ID.String(),
		"caller_phone":     contact.PhoneNumber,
		"whatsapp_account": f.account.Name,
	}
}

func TestPhonesRingForCalls(t *testing.T) {
	t.Run("a call offered to someone rings their phones with a silent, short-lived push", func(t *testing.T) {
		f := newPushFixture(t)
		nurse := f.userWithRole(t, callTaker)
		f.phone(t, nurse, "nurse-phone")
		f.phone(t, nurse, "nurse-tablet")
		other := f.userWithRole(t, callTaker)
		f.phone(t, other, "other-phone")
		contact := f.contact(t, func(c *models.Contact) { c.ProfileName = "EN · Layla Hassan" })
		offer := f.offer(contact)

		f.app.RingPhones(f.org.ID, []uuid.UUID{nurse.ID}, offer)

		assert.ElementsMatch(t, []string{"nurse-phone", "nurse-tablet"}, f.sentTypes()["call"])
		f.fcm.mu.Lock()
		msg := f.fcm.messages[0]
		f.fcm.mu.Unlock()
		assert.NotContains(t, msg, "notification", "the app shows its own ringing")
		android := msg["android"].(map[string]any)
		assert.Equal(t, "HIGH", android["priority"])
		assert.Equal(t, "30s", android["ttl"])
		data := msg["data"].(map[string]any)
		assert.Equal(t, offer["id"], data["transfer_id"])
		assert.Equal(t, "EN · Layla Hassan", data["caller_name"])
		assert.Equal(t, contact.ID.String(), data["contact_id"])
		assert.NotEmpty(t, data["offered_at"])
	})

	t.Run("offered to everyone, only staff who may take calls ring, on phones set to ring", func(t *testing.T) {
		f := newPushFixture(t)
		taker := f.userWithRole(t, callTaker)
		f.phone(t, taker, "taker-phone")
		f.phone(t, taker, "taker-chats-only", func(d *models.MobileDevice) { d.Rings = false })
		f.phone(t, taker, "taker-old", func(d *models.MobileDevice) { now := d.LastSeenAt; d.RevokedAt = &now })
		chatOnly := f.userWithRole(t, chatAgent)
		f.phone(t, chatOnly, "chat-agent-phone")
		require.NoError(t, f.app.DB.Model(&models.MobileDevice{}).Where("push_token = ?", "taker-chats-only").Update("rings", false).Error)

		f.app.RingPhones(f.org.ID, nil, f.offer(f.contact(t)))

		assert.Equal(t, []string{"taker-phone"}, f.sentTypes()["call"])
	})

	t.Run("once someone takes the call, every phone that rang stops", func(t *testing.T) {
		f := newPushFixture(t)
		a, b := f.userWithRole(t, callTaker), f.userWithRole(t, callTaker)
		f.phone(t, a, "a-phone")
		f.phone(t, b, "b-phone")
		offer := f.offer(f.contact(t))
		f.app.RingPhones(f.org.ID, nil, offer)

		f.app.StopRinging(f.org.ID, offer["id"].(string), nil)
		assert.ElementsMatch(t, []string{"a-phone", "b-phone"}, f.sentTypes()["call_end"])

		f.app.RingPhones(f.org.ID, nil, offer)
		assert.Len(t, f.sentTypes()["call"], 2, "a ring arriving after the call is over doesn't ring")
	})

	t.Run("when the call moves on from someone, only their phone stops", func(t *testing.T) {
		f := newPushFixture(t)
		a, b := f.userWithRole(t, callTaker), f.userWithRole(t, callTaker)
		f.phone(t, a, "a-phone")
		f.phone(t, b, "b-phone")
		offer := f.offer(f.contact(t))
		f.app.RingPhones(f.org.ID, []uuid.UUID{a.ID}, offer)
		f.app.StopRinging(f.org.ID, offer["id"].(string), []uuid.UUID{a.ID})
		f.app.RingPhones(f.org.ID, []uuid.UUID{b.ID}, offer)

		assert.Equal(t, []string{"a-phone"}, f.sentTypes()["call_end"])
		assert.Equal(t, []string{"a-phone", "b-phone"}, f.sentTypes()["call"])
	})

	t.Run("a phone can turn ringing off and on", func(t *testing.T) {
		f := newPushFixture(t)
		taker := f.userWithRole(t, callTaker)
		device := f.phone(t, taker, "taker-phone")
		set := func(rings bool) int {
			req := testutil.NewJSONRequest(t, map[string]any{"rings": rings})
			testutil.SetAuthContext(req, f.org.ID, taker.ID)
			req.RequestCtx.SetUserValue(middleware.ContextKeyDeviceID, device.ID)
			require.NoError(t, f.app.UpdateMobileRing(req))
			return testutil.GetResponseStatusCode(req)
		}

		require.Equal(t, fasthttp.StatusOK, set(false))
		f.app.RingPhones(f.org.ID, nil, f.offer(f.contact(t)))
		assert.Empty(t, f.sentTypes()["call"])

		require.Equal(t, fasthttp.StatusOK, set(true))
		f.app.RingPhones(f.org.ID, nil, f.offer(f.contact(t)))
		assert.Equal(t, []string{"taker-phone"}, f.sentTypes()["call"])
	})

	t.Run("a browser can't change a phone's ringing", func(t *testing.T) {
		f := newPushFixture(t)
		req := testutil.NewJSONRequest(t, map[string]any{"rings": false})
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		require.NoError(t, f.app.UpdateMobileRing(req))
		assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
	})
}
