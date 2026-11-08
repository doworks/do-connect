package handlers

import (
	"strings"
	"testing"
	"time"

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

func TestCallTakersAndDecline(t *testing.T) {
	t.Run("only active staff allowed to take calls count as everyone", func(t *testing.T) {
		f := newPushFixture(t)
		taker := f.userWithRole(t, callTaker)
		chatOnly := f.userWithRole(t, chatAgent)
		ids := f.app.CallTakers(f.org.ID)
		assert.Contains(t, ids, taker.ID)
		assert.NotContains(t, ids, chatOnly.ID)
	})

	t.Run("declining a call that doesn't exist says so", func(t *testing.T) {
		f := newPushFixture(t)
		taker := f.userWithRole(t, callTaker)
		req := testutil.NewJSONRequest(t, map[string]any{})
		testutil.SetAuthContext(req, f.org.ID, taker.ID)
		testutil.SetPathParam(req, "id", uuid.NewString())
		require.NoError(t, f.app.DeclineCallTransfer(req))
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
	})

	t.Run("staff who can't take calls can't decline them", func(t *testing.T) {
		f := newPushFixture(t)
		chatOnly := f.userWithRole(t, chatAgent)
		req := testutil.NewJSONRequest(t, map[string]any{})
		testutil.SetAuthContext(req, f.org.ID, chatOnly.ID)
		testutil.SetPathParam(req, "id", uuid.NewString())
		require.NoError(t, f.app.DeclineCallTransfer(req))
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
	})
}

func TestDeclinePass(t *testing.T) {
	decline := func(t *testing.T, f pushFixture, transferID, pass string) int {
		t.Helper()
		req := testutil.NewJSONRequest(t, map[string]any{"transfer_id": transferID, "pass": pass})
		require.NoError(t, f.app.MobileCallDecline(req))
		return testutil.GetResponseStatusCode(req)
	}

	t.Run("each ring carries a pass for its own user, which declines without a sign-in", func(t *testing.T) {
		f := newPushFixture(t)
		taker := f.userWithRole(t, callTaker)
		f.phone(t, taker, "taker-phone")
		contact := f.contact(t)
		offer := f.offer(contact)
		callLog := models.CallLog{
			BaseModel:       models.BaseModel{ID: uuid.New()},
			OrganizationID:  f.org.ID,
			WhatsAppAccount: f.account.Name,
			ContactID:       contact.ID,
			WhatsAppCallID:  "wacid." + uuid.NewString(),
			CallerPhone:     contact.PhoneNumber,
			Status:          models.CallStatusRinging,
		}
		require.NoError(t, f.app.DB.Create(&callLog).Error)
		transfer := models.CallTransfer{
			BaseModel:      models.BaseModel{ID: uuid.MustParse(offer["id"].(string))},
			OrganizationID: f.org.ID,
			CallLogID:      callLog.ID,
			ContactID:      contact.ID,
			CallerPhone:    contact.PhoneNumber,
			Status:         models.CallTransferStatusWaiting,
		}
		require.NoError(t, f.app.DB.Create(&transfer).Error)

		f.app.RingPhones(f.org.ID, []uuid.UUID{taker.ID}, offer)
		f.fcm.mu.Lock()
		data := f.fcm.messages[0]["data"].(map[string]any)
		f.fcm.mu.Unlock()
		pass, _ := data["decline_pass"].(string)
		require.NotEmpty(t, pass)
		assert.Contains(t, pass, taker.ID.String())

		assert.Equal(t, fasthttp.StatusOK, decline(t, f, offer["id"].(string), pass))
	})

	t.Run("a pass for another call, tampered with, or expired is refused", func(t *testing.T) {
		f := newPushFixture(t)
		taker := f.userWithRole(t, callTaker)
		transferID := uuid.NewString()
		good := f.app.declinePass(transferID, taker.ID, time.Now().Add(time.Minute).Unix())

		assert.Equal(t, fasthttp.StatusUnauthorized, decline(t, f, uuid.NewString(), good), "another call")
		assert.Equal(t, fasthttp.StatusUnauthorized, decline(t, f, transferID, good[:len(good)-2]+"00"), "tampered")
		other := f.userWithRole(t, callTaker)
		forged := other.ID.String() + good[strings.Index(good, "."):]
		assert.Equal(t, fasthttp.StatusUnauthorized, decline(t, f, transferID, forged), "someone else's name on it")
		expired := f.app.declinePass(transferID, taker.ID, time.Now().Add(-time.Second).Unix())
		assert.Equal(t, fasthttp.StatusUnauthorized, decline(t, f, transferID, expired))
		assert.Equal(t, fasthttp.StatusUnauthorized, decline(t, f, transferID, "nonsense"))
	})
}
