package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/assignment"
	"github.com/shridarpatil/whatomate/internal/callhours"
	"github.com/shridarpatil/whatomate/internal/calling"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type callHoursFixture struct {
	heldFixture
	hub *websocket.Hub
}

func newCallHoursFixture(t *testing.T) callHoursFixture {
	t.Helper()
	f := newHeldFixture(t)
	if f.app.Redis == nil {
		t.Skip("TEST_REDIS_URL not set, skipping test")
	}
	hub := websocket.NewHub(f.app.Log)
	go hub.Run()
	f.app.WSHub = hub
	f.app.CallManager = calling.NewManager(&config.CallingConfig{}, nil, f.app.DB, f.app.Redis, f.app.WhatsApp, hub,
		assignment.New(f.app.DB, f.app.Redis, f.app.Log), f.app.Log)
	return callHoursFixture{heldFixture: f, hub: hub}
}

// staffMember adds someone who can take calls, signed in unless away is set.
func (f callHoursFixture) staffMember(t *testing.T, name string) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, f.app.DB, f.org.ID, "Reception "+uuid.NewString()[:6], []string{"chat:read"})
	return testutil.CreateTestUser(t, f.app.DB, f.org.ID, testutil.WithRoleID(&role.ID), testutil.WithFullName(name))
}

// signIn connects a user and returns what reaches them.
func (f callHoursFixture) signIn(t *testing.T, user *models.User) *websocket.Client {
	t.Helper()
	client := websocket.NewClient(f.hub, nil, user.ID, f.org.ID)
	f.hub.Register(client)
	testutil.AssertEventually(t, func() bool { return f.hub.IsUserOnline(f.org.ID, user.ID) }, 2*time.Second, "user online")
	return client
}

// received reports whether a message of this kind reached the client.
func received(client *websocket.Client, kind string) bool {
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case raw := <-client.SendChan():
			var message websocket.WSMessage
			if json.Unmarshal(raw, &message) == nil && message.Type == kind {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// closedAllWeek is calling hours under which the clinic is never open, so the
// tests don't depend on when they run.
func closedAllWeek(change ...func(*callhours.Rules)) callhours.Rules {
	rules := callhours.Defaults()
	for i := range rules.Hours {
		rules.Hours[i].Enabled = false
		rules.Hours[i].Ranges = []callhours.TimeRange{}
	}
	rules.Messages.Closed = callhours.ByLanguage{EN: "We're closed. We open {next_open}.", AR: "نحن مغلقون. نفتح {next_open}."}
	for _, fn := range change {
		fn(&rules)
	}
	return rules
}

func ringMode(user *models.User, mode callhours.RingMode) callhours.Staff {
	return callhours.Staff{ID: user.ID.String(), Name: user.FullName, Mode: mode}
}

func (f callHoursFixture) setRules(t *testing.T, rules callhours.Rules) {
	t.Helper()
	require.NoError(t, callhours.Save(f.app.DB, f.org.ID, rules))
}

// call sends one of Meta's call events for a call from this contact.
func (f callHoursFixture) call(t *testing.T, contact *models.Contact, callID, event string) *models.CallLog {
	t.Helper()
	f.app.processCallWebhook(f.account.PhoneID, map[string]any{
		"id":      callID,
		"from":    strings.TrimPrefix(contact.PhoneNumber, "+"),
		"event":   event,
		"session": map[string]any{"sdp_type": "offer", "sdp": "v=0"},
	})
	f.app.WaitForBackgroundTasks()
	var callLog models.CallLog
	require.NoError(t, f.app.DB.Where("whatsapp_call_id = ? AND organization_id = ?", callID, f.org.ID).First(&callLog).Error)
	return &callLog
}

func (f callHoursFixture) rejected() int {
	f.meta.mu.Lock()
	defer f.meta.mu.Unlock()
	count := 0
	for _, request := range f.meta.sent {
		if request["action"] == "reject" {
			count++
		}
	}
	return count
}

func (f callHoursFixture) chat(t *testing.T, contact *models.Contact) (models.Contact, []models.Message) {
	t.Helper()
	var fresh models.Contact
	require.NoError(t, f.app.DB.First(&fresh, contact.ID).Error)
	var messages []models.Message
	require.NoError(t, f.app.DB.Where("contact_id = ?", contact.ID).Order("created_at").Find(&messages).Error)
	return fresh, messages
}

func TestCallRulesEndpoints(t *testing.T) {
	f := newCallHoursFixture(t)
	f.configure(t)
	nurse := f.staffMember(t, "Sara Khan")

	get := func(user *models.User) (int, callhours.Rules) {
		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, f.org.ID, user.ID)
		require.NoError(t, f.app.GetCallRules(req))
		var resp struct {
			Data callhours.Rules `json:"data"`
		}
		_ = json.Unmarshal(testutil.GetResponseBody(req), &resp)
		return testutil.GetResponseStatusCode(req), resp.Data
	}
	put := func(user *models.User, body any) (int, string) {
		req := testutil.NewJSONRequest(t, body)
		testutil.SetAuthContext(req, f.org.ID, user.ID)
		require.NoError(t, f.app.UpdateCallRules(req))
		return testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req))
	}

	t.Run("shows defaults and everyone who can take calls", func(t *testing.T) {
		status, rules := get(f.user)
		require.Equal(t, fasthttp.StatusOK, status)
		assert.Equal(t, "UTC", rules.Timezone)
		assert.Len(t, rules.Hours, 7)
		assert.Equal(t, float64(callhours.DefaultGapHours), rules.MessageGapHours)
		require.Len(t, rules.Staff, 2)
		assert.Equal(t, "Noor Ali", rules.Staff[0].Name)
		assert.Equal(t, callhours.ModeClinic, rules.Staff[0].Mode)
		assert.True(t, rules.Staff[0].Available)
		assert.Len(t, rules.Staff[0].Devices, 1)
	})

	t.Run("saves and reads back", func(t *testing.T) {
		_, rules := get(f.user)
		rules.ClosedNow = true
		rules.ClosedDates = []callhours.ClosedDate{{Date: "2026-12-25", Label: "Holiday"}, {Date: "", Label: "unfinished row"}}
		rules.Templates.Closed = callhours.ByLanguage{EN: " closed_en ", AR: "closed_ar"}
		rules.Staff[1].Mode = callhours.ModeAlways
		rules.Staff[1].Devices[0].Mode = callhours.DeviceNever
		status, body := put(f.user, rules)
		require.Equal(t, fasthttp.StatusOK, status, body)

		_, saved := get(f.user)
		assert.True(t, saved.ClosedNow)
		assert.Equal(t, []callhours.ClosedDate{{Date: "2026-12-25", Label: "Holiday"}}, saved.ClosedDates)
		assert.Equal(t, "closed_en", saved.Templates.Closed.EN)
		assert.Equal(t, nurse.ID.String(), saved.Staff[1].ID)
		assert.Equal(t, callhours.ModeAlways, saved.Staff[1].Mode)
		assert.Equal(t, callhours.DeviceNever, saved.Staff[1].Devices[0].Mode)

		// Other settings are left as they were.
		var org models.Organization
		require.NoError(t, f.app.DB.First(&org, f.org.ID).Error)
		assert.Equal(t, "unlock_en", org.Settings["unlock_template_en"])
	})

	t.Run("refuses hours that make no sense", func(t *testing.T) {
		_, rules := get(f.user)
		rules.Hours[1].Enabled = true
		rules.Hours[1].Ranges = []callhours.TimeRange{{Start: "13:00", End: "09:00"}}
		status, body := put(f.user, rules)
		assert.Equal(t, fasthttp.StatusBadRequest, status)
		assert.Contains(t, body, "ends before it starts")
	})

	t.Run("needs the settings permission", func(t *testing.T) {
		status, _ := get(nurse)
		assert.Equal(t, fasthttp.StatusForbidden, status)
		status, _ = put(nurse, callhours.Defaults())
		assert.Equal(t, fasthttp.StatusForbidden, status)
	})
}

func TestCallsOutsideHours(t *testing.T) {
	t.Run("declined, and the patient is told once in the gap", func(t *testing.T) {
		f := newCallHoursFixture(t)
		f.setRules(t, closedAllWeek())
		contact := f.contact(t, recentlyWrote)

		first := f.call(t, contact, "wacid.1", "connect")
		second := f.call(t, contact, "wacid.2", "connect")

		assert.Equal(t, models.CallStatusMissed, first.Status)
		assert.Equal(t, models.CallStatusMissed, second.Status)
		assert.Nil(t, first.AnsweredAt, "a declined call is never answered")
		assert.Equal(t, 2, f.rejected())

		texts := f.meta.ofType("text")
		require.Len(t, texts, 1, "the second call within the gap sends nothing")
		body := texts[0]["text"].(map[string]any)["body"]
		assert.Equal(t, "We're closed. We open soon.", body)

		fresh, messages := f.chat(t, contact)
		assert.NotNil(t, fresh.WaitingSince, "the chat waits for staff")
		calls := 0
		for _, message := range messages {
			if message.MessageType == models.MessageTypeCall {
				calls++
			}
		}
		assert.Equal(t, 2, calls, "both missed calls show in the chat")

		// Meta's own end event doesn't send another message.
		f.call(t, contact, "wacid.2", "terminate")
		assert.Len(t, f.meta.ofType("text"), 1)
	})

	t.Run("a new gap allows another message", func(t *testing.T) {
		f := newCallHoursFixture(t)
		f.setRules(t, closedAllWeek())
		contact := f.contact(t, recentlyWrote)
		f.call(t, contact, "wacid.1", "connect")
		require.NoError(t, f.app.DB.Model(&models.Message{}).Where("contact_id = ?", contact.ID).
			Update("created_at", time.Now().Add(-7*time.Hour)).Error)

		f.call(t, contact, "wacid.2", "connect")
		assert.Len(t, f.meta.ofType("text"), 2)
	})

	t.Run("outside the 24-hour window the template in their language goes", func(t *testing.T) {
		f := newCallHoursFixture(t)
		require.NoError(t, f.app.DB.Create(&models.Template{
			BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: f.org.ID, WhatsAppAccount: f.account.Name,
			Name: "closed_ar", Language: "ar", Category: "UTILITY", Status: "APPROVED", BodyContent: "مغلقون. نفتح {{1}}.",
		}).Error)
		f.setRules(t, closedAllWeek(func(r *callhours.Rules) {
			r.Templates.Closed = callhours.ByLanguage{EN: "closed_en", AR: "closed_ar"}
		}))
		contact := f.contact(t, func(c *models.Contact) { c.Tags = models.JSONBArray{"Arabic"} })

		f.call(t, contact, "wacid.1", "connect")

		assert.Empty(t, f.meta.ofType("text"))
		templates := f.meta.ofType("template")
		require.Len(t, templates, 1)
		template := templates[0]["template"].(map[string]any)
		assert.Equal(t, "closed_ar", template["name"])
		params, _ := json.Marshal(template["components"])
		assert.Contains(t, string(params), "قريباً", "the opening time fills the variable, in Arabic")
	})

	t.Run("with no way to reach them, only the missed call is recorded", func(t *testing.T) {
		f := newCallHoursFixture(t)
		f.setRules(t, closedAllWeek())
		contact := f.contact(t)

		callLog := f.call(t, contact, "wacid.1", "connect")

		assert.Equal(t, models.CallStatusMissed, callLog.Status)
		assert.Empty(t, f.meta.ofType("text"))
		assert.Empty(t, f.meta.ofType("template"))
		fresh, _ := f.chat(t, contact)
		assert.NotNil(t, fresh.WaitingSince)
	})

	t.Run("on-call staff are still rung, and only they", func(t *testing.T) {
		f := newCallHoursFixture(t)
		onCall := f.staffMember(t, "On Call")
		dayShift := f.staffMember(t, "Day Shift")
		f.setRules(t, closedAllWeek(func(r *callhours.Rules) {
			r.Staff = []callhours.Staff{ringMode(onCall, callhours.ModeAlways), ringMode(dayShift, callhours.ModeClinic)}
		}))
		onCallPhone, dayPhone := f.signIn(t, onCall), f.signIn(t, dayShift)
		contact := f.contact(t, recentlyWrote)

		ringing := f.call(t, contact, "wacid.1", "ringing")

		assert.Equal(t, models.CallStatusRinging, ringing.Status)
		assert.True(t, received(onCallPhone, websocket.TypeCallIncoming))
		assert.False(t, received(dayPhone, websocket.TypeCallIncoming))
		assert.Zero(t, f.rejected())
		assert.Empty(t, f.meta.ofType("text"))
	})

	t.Run("Away wins over on call", func(t *testing.T) {
		f := newCallHoursFixture(t)
		onCall := f.staffMember(t, "On Call")
		require.NoError(t, f.app.DB.Model(onCall).Update("is_available", false).Error)
		f.setRules(t, closedAllWeek(func(r *callhours.Rules) {
			r.Staff = []callhours.Staff{ringMode(onCall, callhours.ModeAlways)}
		}))
		phone := f.signIn(t, onCall)
		contact := f.contact(t, recentlyWrote)

		callLog := f.call(t, contact, "wacid.1", "ringing")

		assert.Equal(t, models.CallStatusMissed, callLog.Status)
		assert.False(t, received(phone, websocket.TypeCallIncoming))
		assert.Equal(t, 1, f.rejected())
	})

	t.Run("close for everyone declines even with on-call staff signed in", func(t *testing.T) {
		f := newCallHoursFixture(t)
		onCall := f.staffMember(t, "On Call")
		rules := callhours.Defaults()
		rules.ClosedNow = true
		rules.Staff = []callhours.Staff{ringMode(onCall, callhours.ModeAlways)}
		f.setRules(t, rules)
		phone := f.signIn(t, onCall)
		contact := f.contact(t, recentlyWrote)

		callLog := f.call(t, contact, "wacid.1", "ringing")
		f.call(t, contact, "wacid.1", "connect")

		assert.Equal(t, models.CallStatusMissed, callLog.Status)
		assert.False(t, received(phone, websocket.TypeCallIncoming))
		assert.Equal(t, 1, f.rejected(), "the connect after a decline is left alone")
		assert.Len(t, f.meta.ofType("text"), 1)
	})

	t.Run("without calling hours nothing changes", func(t *testing.T) {
		f := newCallHoursFixture(t)
		anyone := f.staffMember(t, "Anyone")
		phone := f.signIn(t, anyone)
		contact := f.contact(t, recentlyWrote)

		callLog := f.call(t, contact, "wacid.1", "ringing")

		assert.Equal(t, models.CallStatusRinging, callLog.Status)
		assert.True(t, received(phone, websocket.TypeCallIncoming))
		assert.Zero(t, f.rejected())
	})

	t.Run("a call nobody answers in hours gets the missed call message", func(t *testing.T) {
		f := newCallHoursFixture(t)
		// Ringing as usual outside hours counts as open for this.
		f.setRules(t, closedAllWeek(func(r *callhours.Rules) { r.OutsideAction = callhours.OutsideRing }))
		contact := f.contact(t, recentlyWrote)

		f.call(t, contact, "wacid.1", "ringing")
		callLog := f.call(t, contact, "wacid.1", "missed")

		assert.Equal(t, models.CallStatusMissed, callLog.Status)
		assert.Zero(t, f.rejected())
		texts := f.meta.ofType("text")
		require.Len(t, texts, 1)
		assert.Equal(t, callhours.Defaults().Messages.Missed.EN, texts[0]["text"].(map[string]any)["body"])
		fresh, _ := f.chat(t, contact)
		assert.NotNil(t, fresh.WaitingSince)
	})
}

// screenCall at fixed times, Tuesday 3 Nov 2026 in Bahrain.
func TestScreenCall(t *testing.T) {
	f := newCallHoursFixture(t)
	require.NoError(t, f.app.DB.Model(f.org).Update("settings", models.JSONB{"timezone": "Asia/Bahrain"}).Error)
	clinic := f.staffMember(t, "Clinic")
	away := f.staffMember(t, "Away")
	require.NoError(t, f.app.DB.Model(away).Update("is_available", false).Error)
	onCall := f.staffMember(t, "On Call")
	rules := callhours.Defaults()
	rules.Staff = []callhours.Staff{
		ringMode(clinic, callhours.ModeClinic), ringMode(away, callhours.ModeClinic), ringMode(onCall, callhours.ModeAlways),
		ringMode(f.user, callhours.ModeNever),
	}
	f.setRules(t, rules)
	at := func(clock string) time.Time {
		moment, err := time.Parse(time.RFC3339, "2026-11-03T"+clock+":00+03:00")
		require.NoError(t, err)
		return moment
	}

	open := f.app.screenCall(f.org.ID, false, at("10:00"))
	assert.False(t, open.decline)
	assert.ElementsMatch(t, []uuid.UUID{clinic.ID, onCall.ID}, open.ring)

	// After hours with nobody signed in, the call is declined.
	closed := f.app.screenCall(f.org.ID, false, at("22:00"))
	assert.True(t, closed.decline)
	assert.Equal(t, []uuid.UUID{onCall.ID}, closed.ring)

	f.signIn(t, onCall)
	assert.False(t, f.app.screenCall(f.org.ID, false, at("22:00")).decline)

	// A click-to-call call after hours belongs to the person who asked for it.
	assert.False(t, f.app.screenCall(f.org.ID, true, at("03:00")).decline)
}
