package handlers

import (
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// The sound the web app plays for a new message. The clinic turns it on or
// off for everyone and picks the usual sound, loudness and when it plays; each
// person can then choose their own, or keep the clinic's.

// messageSounds are the sounds the web app ships (frontend/public/sounds).
var messageSounds = map[string]bool{"chime": true, "pop": true, "bell": true, "drop": true, "classic": true}

// When the sound plays: "away" only while nobody is looking at Do Connect,
// "always" also while someone is in it, for chats other than the open one.
var messageSoundWhen = map[string]bool{"away": true, "always": true}

const (
	defaultMessageSound       = "chime"
	defaultMessageSoundVolume = 60
	defaultMessageSoundWhen   = "always"
)

// MessageSoundSettings is the clinic's choice, in the organisation settings.
type MessageSoundSettings struct {
	Enabled bool   `json:"message_sound_enabled"`
	Sound   string `json:"message_sound"`
	Volume  int    `json:"message_sound_volume"`
	When    string `json:"message_sound_when"`
}

func clinicMessageSound(settings models.JSONB) MessageSoundSettings {
	out := MessageSoundSettings{Enabled: true, Sound: defaultMessageSound, Volume: defaultMessageSoundVolume, When: defaultMessageSoundWhen}
	if v, ok := settings["message_sound_enabled"].(bool); ok {
		out.Enabled = v
	}
	if v, ok := settings["message_sound"].(string); ok && messageSounds[v] {
		out.Sound = v
	}
	if v, ok := settings["message_sound_volume"].(float64); ok && v >= 0 && v <= 100 {
		out.Volume = int(v)
	} else if v, ok := settings["message_sound_volume"].(int); ok && v >= 0 && v <= 100 {
		out.Volume = v
	}
	if v, ok := settings["message_sound_when"].(string); ok && messageSoundWhen[v] {
		out.When = v
	}
	return out
}

// messageSoundUpdate is the clinic part of an organisation settings save.
type messageSoundUpdate struct {
	Enabled *bool   `json:"message_sound_enabled"`
	Sound   *string `json:"message_sound"`
	Volume  *int    `json:"message_sound_volume"`
	When    *string `json:"message_sound_when"`
}

func (u messageSoundUpdate) touched() bool {
	return u.Enabled != nil || u.Sound != nil || u.Volume != nil || u.When != nil
}

// problem is what's wrong with the update, for the person saving it.
func (u messageSoundUpdate) problem() string {
	switch {
	case u.Sound != nil && !messageSounds[*u.Sound]:
		return "Choose one of the message sounds"
	case u.Volume != nil && (*u.Volume < 0 || *u.Volume > 100):
		return "The volume goes from 0 to 100"
	case u.When != nil && !messageSoundWhen[*u.When]:
		return "Choose when the message sound plays"
	}
	return ""
}

func (u messageSoundUpdate) apply(settings models.JSONB) {
	if u.Enabled != nil {
		settings["message_sound_enabled"] = *u.Enabled
	}
	if u.Sound != nil {
		settings["message_sound"] = *u.Sound
	}
	if u.Volume != nil {
		settings["message_sound_volume"] = *u.Volume
	}
	if u.When != nil {
		settings["message_sound_when"] = *u.When
	}
}

// MyMessageSoundRequest is one person's own choice. Empty sound or when, and
// no volume, mean "the clinic's"; sound "off" means no sound for them.
type MyMessageSoundRequest struct {
	Sound  string `json:"message_sound"`
	Volume *int   `json:"message_sound_volume"`
	When   string `json:"message_sound_when"`
}

// UpdateMyMessageSound saves the signed-in person's message sound.
func (a *App) UpdateMyMessageSound(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	var req MyMessageSoundRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	if req.Sound != "" && req.Sound != "off" && !messageSounds[req.Sound] {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Choose one of the message sounds", nil, "")
	}
	if req.Volume != nil && (*req.Volume < 0 || *req.Volume > 100) {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "The volume goes from 0 to 100", nil, "")
	}
	if req.When != "" && !messageSoundWhen[req.When] {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Choose when the message sound plays", nil, "")
	}

	var user models.User
	if err := a.DB.Where("id = ?", userID).First(&user).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "User not found", nil, "")
	}
	if user.Settings == nil {
		user.Settings = models.JSONB{}
	}
	old := notificationSettingsSnapshot(user.Settings)
	setOrClear := func(key string, value any, keep bool) {
		if keep {
			user.Settings[key] = value
		} else {
			delete(user.Settings, key)
		}
	}
	setOrClear("message_sound", req.Sound, req.Sound != "")
	if req.Volume != nil {
		user.Settings["message_sound_volume"] = *req.Volume
	} else {
		delete(user.Settings, "message_sound_volume")
	}
	setOrClear("message_sound_when", req.When, req.When != "")

	if err := a.DB.Model(&user).Update("settings", user.Settings).Error; err != nil {
		a.Log.Error("Failed to save message sound", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to save the message sound", nil, "")
	}
	a.logAudit(orgID, userID, models.ResourceSettingsNotification, userID, models.AuditActionUpdated, old, notificationSettingsSnapshot(user.Settings))
	return r.SendEnvelope(map[string]any{"settings": user.Settings})
}
