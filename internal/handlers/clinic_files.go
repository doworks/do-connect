package handlers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// Saving a patient's photo or document into the clinic system. Whatomate sends
// its stored copy through the signed panel link, so the clinic system saves it
// as the staff member, and the message is marked so nobody saves it twice.

const maxClinicFileBytes = 25 << 20

type SaveToClinicRequest struct {
	Patient string `json:"patient"`
	Kind    string `json:"kind"`
	Note    string `json:"note"`
}

// SavedToClinic is what a message remembers about being saved.
type SavedToClinic struct {
	Patient     string    `json:"patient"`
	PatientName string    `json:"patient_name,omitempty"`
	Kind        string    `json:"kind"`
	File        string    `json:"file,omitempty"`
	ByUserID    uuid.UUID `json:"by_user_id"`
	ByName      string    `json:"by_name"`
	At          time.Time `json:"at"`
}

// SaveMessageToClinic sends a message's photo or file to the patient's record.
func (a *App) SaveMessageToClinic(r *fastglue.Request) error {
	cfg := a.Config.Clinic
	if !cfg.Enabled() {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "The patient panel isn't set up", nil, "")
	}
	orgID, userID, err := a.requireAuth(r, models.ResourceContacts, models.ActionRead)
	if err != nil {
		return nil
	}
	if viaKey, _ := r.RequestCtx.UserValue(middleware.ContextKeyViaAPIKey).(bool); viaKey {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Saving to a patient's file is for signed-in staff", nil, "")
	}
	messageID, err := parsePathUUID(r, "message_id", "message")
	if err != nil {
		return nil
	}
	var req SaveToClinicRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil || req.Patient == "" || req.Kind == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Choose the patient and what this is", nil, "")
	}

	message, err := findByIDAndOrg[models.Message](a.DB, r, messageID, orgID, "Message")
	if err != nil {
		return nil
	}
	if !forwardableMedia[message.MessageType] || message.MediaURL == "" {
		return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, "Only a saved photo or file can go to the patient's file", nil, "")
	}
	data, err := a.readStoredMedia(message.MediaURL)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, "This file isn't saved in Whatomate", nil, "")
	}
	if len(data) > maxClinicFileBytes {
		return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, "This file is over 25 MB, too big for the patient's file", nil, "")
	}

	var user models.User
	if err := a.DB.Select("id", "email", "full_name").Where("id = ?", userID).First(&user).Error; err != nil || user.Email == "" {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Your account has no email to match in the clinic system", nil, "")
	}

	filename := mediaFilename(message)
	status, answer, reason := a.callClinic(cfg, clinicRequest{
		Action: "save_file",
		Args: map[string]any{
			"patient":      req.Patient,
			"kind":         req.Kind,
			"note":         req.Note,
			"filename":     filename,
			"content_type": baseMime(message.MediaMimeType),
			"data":         base64.StdEncoding.EncodeToString(data),
			"received_on":  message.CreatedAt.Format("2006-01-02"),
			"caption":      message.Content,
		},
		ActingUser: user.Email,
		Timestamp:  time.Now().Unix(),
	})
	if status != fasthttp.StatusOK {
		return r.SendErrorEnvelope(status, reason, nil, "")
	}

	var saved struct {
		File        string `json:"file"`
		PatientName string `json:"patient_name"`
	}
	_ = json.Unmarshal(answer, &saved)
	marker := SavedToClinic{
		Patient:     req.Patient,
		PatientName: saved.PatientName,
		Kind:        req.Kind,
		File:        saved.File,
		ByUserID:    userID,
		ByName:      user.FullName,
		At:          time.Now(),
	}
	patch, _ := json.Marshal(map[string]any{"saved_to_clinic": marker})
	a.DB.Model(&models.Message{}).Where("id = ?", message.ID).
		Update("metadata", gorm.Expr("COALESCE(metadata, '{}'::jsonb) || ?::jsonb", string(patch)))
	return r.SendEnvelope(marker)
}

func extensionForMime(mime string) string {
	switch baseMime(mime) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	case "video/mp4":
		return ".mp4"
	case "video/3gpp":
		return ".3gp"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/aac":
		return ".aac"
	case "audio/amr":
		return ".amr"
	case "audio/mp4":
		return ".m4a"
	}
	return ""
}

// baseMime drops parameters such as "; codecs=opus", which Meta's upload refuses.
func baseMime(mime string) string {
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	return strings.TrimSpace(strings.ToLower(mime))
}

// mediaFilename is the stored name, or one made from the type. Patients'
// photos, videos and voice notes arrive without a name, and Meta's upload
// treats an empty name as no file at all.
func mediaFilename(message *models.Message) string {
	if message.MediaFilename != "" {
		return message.MediaFilename
	}
	return "whatsapp" + extensionForMime(message.MediaMimeType)
}

// savedToClinicFrom reads the marker back for the chat screen.
func savedToClinicFrom(metadata models.JSONB) *SavedToClinic {
	raw, ok := metadata["saved_to_clinic"]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var marker SavedToClinic
	if json.Unmarshal(encoded, &marker) != nil {
		return nil
	}
	return &marker
}
