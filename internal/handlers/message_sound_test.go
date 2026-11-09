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

func TestMessageSoundSettings(t *testing.T) {
	f := newHeldFixture(t)
	app, org, user := f.app, f.org, f.user

	readClinic := func(t *testing.T) MessageSoundSettings {
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
		return resp.Data.Settings.MessageSoundSettings
	}
	saveClinic := func(t *testing.T, body map[string]any) int {
		t.Helper()
		req := testutil.NewJSONRequest(t, body)
		testutil.SetAuthContext(req, org.ID, user.ID)
		require.NoError(t, app.UpdateOrganizationSettings(req))
		return testutil.GetResponseStatusCode(req)
	}
	saveMine := func(t *testing.T, body map[string]any) int {
		t.Helper()
		req := testutil.NewJSONRequest(t, body)
		testutil.SetAuthContext(req, org.ID, user.ID)
		require.NoError(t, app.UpdateMyMessageSound(req))
		return testutil.GetResponseStatusCode(req)
	}
	mine := func(t *testing.T) models.JSONB {
		t.Helper()
		var u models.User
		require.NoError(t, app.DB.First(&u, "id = ?", user.ID).Error)
		return u.Settings
	}

	t.Run("on, with a soft chime at 60, until the clinic chooses", func(t *testing.T) {
		assert.Equal(t, MessageSoundSettings{Enabled: true, Sound: "chime", Volume: 60, When: "always"}, readClinic(t))
	})

	t.Run("the clinic's choice is saved, other settings untouched", func(t *testing.T) {
		require.Equal(t, fasthttp.StatusOK, saveClinic(t, map[string]any{"mask_phone_numbers": true}))
		require.Equal(t, fasthttp.StatusOK, saveClinic(t, map[string]any{"message_sound_enabled": false, "message_sound": "bell", "message_sound_volume": 25, "message_sound_when": "away"}))
		assert.Equal(t, MessageSoundSettings{Enabled: false, Sound: "bell", Volume: 25, When: "away"}, readClinic(t))
		require.Equal(t, fasthttp.StatusOK, saveClinic(t, map[string]any{"message_sound_volume": 0}))
		assert.Equal(t, 0, readClinic(t).Volume, "silent is a volume too")
		var o models.Organization
		require.NoError(t, app.DB.First(&o, "id = ?", org.ID).Error)
		assert.Equal(t, true, o.Settings["mask_phone_numbers"])
	})

	t.Run("the clinic can't pick a sound that isn't there", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"message_sound": "siren"}, {"message_sound_volume": 101}, {"message_sound_volume": -1}, {"message_sound_when": "never"},
		} {
			assert.Equal(t, fasthttp.StatusBadRequest, saveClinic(t, body), body)
		}
		assert.Equal(t, "bell", readClinic(t).Sound, "unchanged")
	})

	t.Run("a person keeps their own choice, or goes back to the clinic's", func(t *testing.T) {
		require.Equal(t, fasthttp.StatusOK, saveMine(t, map[string]any{"message_sound": "pop", "message_sound_volume": 80, "message_sound_when": "away"}))
		s := mine(t)
		assert.Equal(t, "pop", s["message_sound"])
		assert.EqualValues(t, 80, s["message_sound_volume"])
		assert.Equal(t, "away", s["message_sound_when"])

		require.Equal(t, fasthttp.StatusOK, saveMine(t, map[string]any{"message_sound": "off"}))
		s = mine(t)
		assert.Equal(t, "off", s["message_sound"])
		assert.NotContains(t, s, "message_sound_volume", "no volume means the clinic's")
		assert.NotContains(t, s, "message_sound_when")

		require.Equal(t, fasthttp.StatusOK, saveMine(t, map[string]any{}))
		assert.NotContains(t, mine(t), "message_sound")
	})

	t.Run("a person can't pick a sound that isn't there", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"message_sound": "siren"}, {"message_sound_volume": 150}, {"message_sound_when": "sometimes"},
		} {
			assert.Equal(t, fasthttp.StatusBadRequest, saveMine(t, body), body)
		}
	})

	t.Run("saving the other notification switches keeps the sound", func(t *testing.T) {
		require.Equal(t, fasthttp.StatusOK, saveMine(t, map[string]any{"message_sound": "drop"}))
		req := testutil.NewJSONRequest(t, map[string]any{"email_notifications": true, "new_message_alerts": false, "campaign_updates": true})
		testutil.SetAuthContext(req, org.ID, user.ID)
		require.NoError(t, app.UpdateCurrentUserSettings(req))
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
		assert.Equal(t, "drop", mine(t)["message_sound"])
	})
}
