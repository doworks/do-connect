package handlers

import (
	"encoding/json"
	"testing"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestIncomingCallModeSetting(t *testing.T) {
	f := newHeldFixture(t)
	app, org, user := f.app, f.org, f.user

	read := func(t *testing.T) OrganizationSettings {
		t.Helper()
		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, org.ID, user.ID)
		require.NoError(t, app.GetOrganizationSettings(req))
		var resp struct {
			Data struct {
				Settings OrganizationSettings `json:"settings"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
		return resp.Data.Settings
	}
	save := func(t *testing.T, body map[string]any) int {
		t.Helper()
		req := testutil.NewJSONRequest(t, body)
		testutil.SetAuthContext(req, org.ID, user.ID)
		require.NoError(t, app.UpdateOrganizationSettings(req))
		return testutil.GetResponseStatusCode(req)
	}

	t.Run("calls are answered straight away until the clinic chooses otherwise", func(t *testing.T) {
		s := read(t)
		assert.Equal(t, "answer", s.IncomingCallMode)
		assert.Equal(t, "hold", s.RingNoAnswer)
		assert.Empty(t, s.WaitingSound)
	})

	t.Run("the choice is saved", func(t *testing.T) {
		require.Equal(t, fasthttp.StatusOK, save(t, map[string]any{"incoming_call_mode": "ring", "ring_no_answer": "missed", "waiting_sound": "music"}))
		s := read(t)
		assert.Equal(t, "ring", s.IncomingCallMode)
		assert.Equal(t, "missed", s.RingNoAnswer)
		assert.Equal(t, "music", s.WaitingSound)
	})

	t.Run("anything else is refused", func(t *testing.T) {
		assert.Equal(t, fasthttp.StatusBadRequest, save(t, map[string]any{"incoming_call_mode": "voicemail"}))
		assert.Equal(t, fasthttp.StatusBadRequest, save(t, map[string]any{"ring_no_answer": "later"}))
		assert.Equal(t, fasthttp.StatusBadRequest, save(t, map[string]any{"waiting_sound": "jazz"}))
		assert.Equal(t, "ring", read(t).IncomingCallMode, "unchanged")
	})
}

func TestIncomingCallModeAtConnect(t *testing.T) {
	setMode := func(t *testing.T, f callHoursFixture, mode string) {
		t.Helper()
		require.NoError(t, f.app.DB.Model(&models.Organization{}).Where("id = ?", f.org.ID).
			Update("settings", models.JSONB{"calling_enabled": true, "incoming_call_mode": mode}).Error)
	}

	t.Run("answered straight away, the call is answered when it connects", func(t *testing.T) {
		f := newCallHoursFixture(t)
		setMode(t, f, "answer")
		callLog := f.call(t, f.contact(t), "wacid.answer", "connect")
		assert.Equal(t, models.CallStatusAnswered, callLog.Status)
		assert.NotNil(t, callLog.AnsweredAt)
	})

	t.Run("kept ringing, it isn't answered until someone picks up", func(t *testing.T) {
		f := newCallHoursFixture(t)
		setMode(t, f, "ring")
		contact := f.contact(t)
		callLog := f.call(t, contact, "wacid.ring", "connect")
		assert.NotEqual(t, models.CallStatusAnswered, callLog.Status)
		assert.Nil(t, callLog.AnsweredAt)

		// Nobody picked up and the caller gave up: a missed call.
		callLog = f.call(t, contact, "wacid.ring", "terminate")
		assert.Equal(t, models.CallStatusMissed, callLog.Status)
		_, messages := f.chat(t, contact)
		require.NotEmpty(t, messages)
		assert.Equal(t, models.MessageTypeCall, messages[len(messages)-1].MessageType, "the chat shows the missed call")
	})
}
