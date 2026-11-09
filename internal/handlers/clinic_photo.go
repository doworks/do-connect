package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// A chat's avatar is the patient's photo from the clinic system, asked for as
// the staff member looking at it, so their clinic permissions decide whether
// they see it. Who-sees-which-photo is remembered per person for a few hours;
// the picture itself is kept once per version, so a busy chat list doesn't
// keep asking the clinic system or fill the cache with copies.

const (
	photoLookupTTL  = 6 * time.Hour
	photoMissingTTL = time.Hour
	photoFailedTTL  = 5 * time.Minute
	photoImageTTL   = 24 * time.Hour
	photoMaxBytes   = 512 << 10
	photoWait       = 10 * time.Second
	// photoBrowserAge lets the browser reuse a photo for an hour without asking.
	photoBrowserAge = 3600
	// photoNone marks a lookup that found no photo.
	photoNone = "-"
)

// photoVersion is what a version may look like: it goes into a cache key and the ETag.
var photoVersion = regexp.MustCompile(`^[0-9a-f]{6,40}$`)

// clinicPhotoSlots caps how many photo requests reach the clinic system at
// once: the first look at a chat list asks for every chat in it.
var clinicPhotoSlots = make(chan struct{}, 3)

// contactPhotoURL is the avatar address for a contact when photos come from
// the clinic system.
func (a *App) contactPhotoURL(contactID uuid.UUID) string {
	if a.Config == nil || !a.Config.Clinic.Enabled() {
		return ""
	}
	return "/api/contacts/" + contactID.String() + "/photo"
}

// ContactPhoto serves the patient photo for a chat, or 404 when there is none.
func (a *App) ContactPhoto(r *fastglue.Request) error {
	if a.Config == nil || !a.Config.Clinic.Enabled() {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "No photos without the clinic system", nil, "")
	}
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	if viaKey, _ := r.RequestCtx.UserValue(middleware.ContextKeyViaAPIKey).(bool); viaKey {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Patient photos are for signed-in staff", nil, "")
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}
	var contact models.Contact
	query := a.scopeAssignedContact(a.DB.Where("id = ? AND organization_id = ?", contactID, orgID), userID, orgID)
	if err := query.Select("id", "phone_number").First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}
	patient := string(r.RequestCtx.QueryArgs().Peek("patient"))
	return a.sendPhoto(r, orgID, userID, contact.PhoneNumber, patient)
}

// MobileCallPhoto serves the caller's photo to the phone's ringing screen,
// which has the ring's pass but no sign-in while the app is closed.
func (a *App) MobileCallPhoto(r *fastglue.Request) error {
	if a.Config == nil || !a.Config.Clinic.Enabled() {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "No photos without the clinic system", nil, "")
	}
	transferID, err := uuid.Parse(string(r.RequestCtx.QueryArgs().Peek("transfer_id")))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid call", nil, "")
	}
	userID, ok := a.checkDeclinePass(transferID.String(), string(r.RequestCtx.QueryArgs().Peek("pass")))
	if !ok {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "This ring has expired", nil, "")
	}
	var transfer models.CallTransfer
	if err := a.DB.Select("id", "organization_id", "contact_id", "caller_phone").Where("id = ?", transferID).First(&transfer).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Call not found", nil, "")
	}
	return a.sendPhoto(r, transfer.OrganizationID, userID, transfer.CallerPhone, "")
}

func (a *App) sendPhoto(r *fastglue.Request, orgID, userID uuid.UUID, phone, patient string) error {
	image, version := a.clinicPhoto(context.Background(), orgID, userID, phone, patient)
	if image == nil {
		// Not kept by the browser: the answer changes when the person is
		// linked to another staff login, and the server already remembers it.
		r.RequestCtx.Response.Header.Set("Cache-Control", "no-store")
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "No photo", nil, "")
	}
	etag := `"` + version + `"`
	r.RequestCtx.Response.Header.Set("Cache-Control", fmt.Sprintf("private, max-age=%d", photoBrowserAge))
	r.RequestCtx.Response.Header.Set("ETag", etag)
	if string(r.RequestCtx.Request.Header.Peek("If-None-Match")) == etag {
		r.RequestCtx.SetStatusCode(fasthttp.StatusNotModified)
		return nil
	}
	r.RequestCtx.SetContentType("image/jpeg")
	r.RequestCtx.SetBody(image)
	return nil
}

func photoLookupKey(userID uuid.UUID, phone, patient string) string {
	return fmt.Sprintf("clinic:photo:who:%s:%s:%s", userID, phone, patient)
}

func photoImageKey(orgID uuid.UUID, version string) string {
	return fmt.Sprintf("clinic:photo:img:%s:%s", orgID, version)
}

// clinicPhoto returns the photo and its version, from the cache when it can.
func (a *App) clinicPhoto(ctx context.Context, orgID, userID uuid.UUID, phone, patient string) ([]byte, string) {
	lookup := photoLookupKey(userID, phone, patient)
	if a.Redis != nil {
		if version, err := a.Redis.Get(ctx, lookup).Result(); err == nil {
			if version == photoNone {
				return nil, ""
			}
			if image, err := a.Redis.Get(ctx, photoImageKey(orgID, version)).Bytes(); err == nil {
				return image, version
			}
		}
	}

	remember := func(version string, ttl time.Duration) {
		if a.Redis != nil {
			a.Redis.Set(ctx, lookup, version, ttl)
		}
	}
	var user models.User
	if err := a.DB.Select("id", "email", "clinic_user").Where("id = ?", userID).First(&user).Error; err != nil || user.Email == "" {
		remember(photoNone, photoMissingTTL)
		return nil, ""
	}

	select {
	case clinicPhotoSlots <- struct{}{}:
		defer func() { <-clinicPhotoSlots }()
	case <-time.After(photoWait):
		return nil, ""
	}
	status, data, _ := a.callClinic(a.Config.Clinic, clinicRequest{
		Action:     "photo",
		Args:       map[string]any{"phone": phone, "patient": patient},
		ActingUser: clinicLogin(user),
		Timestamp:  time.Now().Unix(),
	})
	if status != fasthttp.StatusOK {
		// The clinic system is down or refused; try again soon, not on every render.
		remember(photoNone, photoFailedTTL)
		return nil, ""
	}
	var found struct {
		Data    string `json:"data"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &found)
	image, err := base64.StdEncoding.DecodeString(found.Data)
	if found.Data == "" || !photoVersion.MatchString(found.Version) || err != nil || len(image) > photoMaxBytes {
		remember(photoNone, photoMissingTTL)
		return nil, ""
	}
	if a.Redis != nil {
		a.Redis.Set(ctx, photoImageKey(orgID, found.Version), image, photoImageTTL)
	}
	remember(found.Version, photoLookupTTL)
	return image, found.Version
}
