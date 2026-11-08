package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Phones sign in once and stay signed in. Browsers keep their session in
// cookies with short-lived refresh tokens; a phone instead gets a device
// record holding one long-lived refresh token, and short-lived access tokens
// sent as a Bearer header.
const (
	// mobileRefreshGrace is how long the refresh token that was just replaced
	// still works, once. A phone whose connection dropped before the new token
	// arrived, or that crashed before saving it, retries with the old one.
	// It stops working sooner as soon as the phone uses the new token.
	mobileRefreshGrace = 7 * 24 * time.Hour
	// mobileDeviceIdleExpiry signs out a phone that has not refreshed for this
	// long.
	mobileDeviceIdleExpiry   = 90 * 24 * time.Hour
	mobileRefreshTokenPrefix = "wmd_"
)

// MobileDeviceInfo describes the phone, as reported by the app.
type MobileDeviceInfo struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
	PushToken  string `json:"push_token"`
}

// MobileLoginRequest is the phone sign-in body.
type MobileLoginRequest struct {
	Email    string           `json:"email"`
	Password string           `json:"password"`
	Device   MobileDeviceInfo `json:"device"`
}

// MobileRefreshRequest exchanges a device refresh token for new tokens.
// PushToken, when sent, replaces the one stored for the device.
type MobileRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	PushToken    string `json:"push_token"`
}

// MobileAuthResponse carries the tokens in the body; phones have no cookies.
type MobileAuthResponse struct {
	AccessToken  string               `json:"access_token"`
	RefreshToken string               `json:"refresh_token"`
	ExpiresIn    int                  `json:"expires_in"`
	DeviceID     uuid.UUID            `json:"device_id"`
	Device       MobileDeviceResponse `json:"device"`
	User         models.User          `json:"user"`
}

// MobileDeviceResponse is a signed-in phone as shown in device lists.
type MobileDeviceResponse struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	UserName    string    `json:"user_name,omitempty"`
	UserEmail   string    `json:"user_email,omitempty"`
	Name        string    `json:"name"`
	Model       string    `json:"model"`
	Platform    string    `json:"platform"`
	AppVersion  string    `json:"app_version"`
	PushEnabled bool      `json:"push_enabled"`
	Rings       bool      `json:"rings"`
	Current     bool      `json:"current"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// MobilePushTokenRequest updates the push token of the calling phone.
type MobilePushTokenRequest struct {
	PushToken string `json:"push_token"`
}

// MobileLogin signs a phone in and creates its device record.
func (a *App) MobileLogin(r *fastglue.Request) error {
	var req MobileLoginRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || req.Password == "" {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Invalid credentials", nil, "")
	}

	var user models.User
	if err := a.DB.Where("email = ?", email).First(&user).Error; err != nil {
		// Same bcrypt cost as a real check so timing does not reveal the account.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"), []byte(req.Password))
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Invalid credentials", nil, "")
	}
	// The password is checked first, so a disabled account is only revealed to
	// someone who already knows its password.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Invalid credentials", nil, "")
	}
	if !user.IsActive {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Account is disabled", nil, "")
	}

	refreshToken, refreshHash, err := newMobileRefreshToken()
	if err != nil {
		a.Log.Error("Failed to generate device token", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to generate token", nil, "")
	}

	now := time.Now()
	device := models.MobileDevice{
		OrganizationID: user.OrganizationID,
		UserID:         user.ID,
		Name:           clip(req.Device.Name, 100),
		Model:          clip(req.Device.Model, 100),
		Platform:       clip(strings.ToLower(req.Device.Platform), 20),
		AppVersion:     clip(req.Device.AppVersion, 40),
		PushToken:      clip(req.Device.PushToken, 4096),
		TokenHash:      refreshHash,
		LastSeenAt:     now,
	}
	if device.Name == "" {
		device.Name = "Phone"
	}
	if err := a.DB.Create(&device).Error; err != nil {
		a.Log.Error("Failed to create device", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to sign in", nil, "")
	}

	// Phones that have not been seen for a long time are already signed out.
	a.DB.Where("user_id = ? AND last_seen_at < ?", user.ID, now.Add(-mobileDeviceIdleExpiry)).Delete(&models.MobileDevice{})

	return a.sendMobileTokens(r, &user, &device, refreshToken)
}

// MobileRefresh rotates a phone's refresh token and issues a new access token.
// Only a 401 means the phone is signed out; anything else is worth retrying.
func (a *App) MobileRefresh(r *fastglue.Request) error {
	var req MobileRefreshRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	if !strings.HasPrefix(req.RefreshToken, mobileRefreshTokenPrefix) {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Invalid refresh token", nil, "")
	}
	presented := hashMobileToken(req.RefreshToken)
	now := time.Now()

	var (
		device       models.MobileDevice
		user         *models.User
		refreshToken string
		signedOut    string
	)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		// The row lock makes two refreshes with the same token take turns, so
		// a token can never be exchanged twice.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? OR (previous_token_hash = ? AND previous_valid_until > ?)", presented, presented, now).
			First(&device).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				signedOut = "Invalid refresh token"
				return nil
			}
			return err
		}
		if device.RevokedAt != nil {
			signedOut = "Device has been signed out"
			return nil
		}
		if device.LastSeenAt.Before(now.Add(-mobileDeviceIdleExpiry)) {
			signedOut = "Session expired"
			return tx.Model(&device).Update("revoked_at", now).Error
		}

		var reason string
		var err error
		user, reason, err = a.mobileDeviceUser(tx, &device)
		if err != nil {
			return err
		}
		if reason != "" {
			signedOut = reason
			return tx.Model(&device).Update("revoked_at", now).Error
		}

		var newHash string
		refreshToken, newHash, err = newMobileRefreshToken()
		if err != nil {
			return err
		}
		updates := map[string]any{
			"token_hash":   newHash,
			"last_seen_at": now,
		}
		if device.TokenHash == presented {
			// Normal rotation: the replaced token stays usable once, for a while.
			updates["previous_token_hash"] = presented
			updates["previous_valid_until"] = now.Add(mobileRefreshGrace)
		} else {
			// The phone never got the newest token; it has now used its one
			// retry with the previous token.
			updates["previous_token_hash"] = nil
			updates["previous_valid_until"] = nil
		}
		if req.PushToken != "" {
			updates["push_token"] = clip(req.PushToken, 4096)
		}
		if err := tx.Model(&device).Updates(updates).Error; err != nil {
			return err
		}
		device.LastSeenAt = now
		return nil
	})
	if err != nil {
		a.Log.Error("Failed to refresh device session", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to refresh session", nil, "")
	}
	if signedOut != "" {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, signedOut, nil, "")
	}

	return a.sendMobileTokens(r, user, &device, refreshToken)
}

// MobileLogout signs the phone out and deletes its device record. It always
// succeeds, so a phone can sign out even with a token the server no longer knows.
func (a *App) MobileLogout(r *fastglue.Request) error {
	var req MobileRefreshRequest
	_ = r.Decode(&req, "json")
	if strings.HasPrefix(req.RefreshToken, mobileRefreshTokenPrefix) {
		hash := hashMobileToken(req.RefreshToken)
		if err := a.DB.Where("token_hash = ? OR previous_token_hash = ?", hash, hash).Delete(&models.MobileDevice{}).Error; err != nil {
			a.Log.Error("Failed to delete device on sign out", "error", err)
		}
	}
	return r.SendEnvelope(map[string]string{"status": "logged_out"})
}

// ListMobileDevices lists signed-in phones: every phone in the organization for
// users who can view users, otherwise only the caller's own. ?scope=mine always
// returns only the caller's own; ?user_id= narrows an admin's list to one user.
func (a *App) ListMobileDevices(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	query := a.DB.Preload("User").
		Where("organization_id = ? AND revoked_at IS NULL", orgID).
		Order("last_seen_at DESC")
	mineOnly := string(r.RequestCtx.QueryArgs().Peek("scope")) == "mine"
	if mineOnly || !a.HasPermission(userID, models.ResourceUsers, models.ActionRead, orgID) {
		query = query.Where("user_id = ?", userID)
	} else if forUser := string(r.RequestCtx.QueryArgs().Peek("user_id")); forUser != "" {
		forUserID, err := uuid.Parse(forUser)
		if err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid user ID", nil, "")
		}
		query = query.Where("user_id = ?", forUserID)
	}

	var devices []models.MobileDevice
	if err := query.Find(&devices).Error; err != nil {
		a.Log.Error("Failed to list devices", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list devices", nil, "")
	}

	current, _ := middleware.GetDeviceID(r)
	items := make([]MobileDeviceResponse, 0, len(devices))
	for i := range devices {
		items = append(items, mobileDeviceToResponse(&devices[i], current))
	}
	return r.SendEnvelope(map[string]any{"devices": items})
}

// DeleteMobileDevice signs a phone out remotely. Anyone may remove their own
// phones; removing someone else's needs permission to edit users.
func (a *App) DeleteMobileDevice(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	id, err := parsePathUUID(r, "id", "device")
	if err != nil {
		return nil
	}

	var device models.MobileDevice
	if err := a.DB.Where("id = ? AND revoked_at IS NULL", id).First(&device).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Device not found", nil, "")
	}
	if device.UserID != userID {
		if device.OrganizationID != orgID {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Device not found", nil, "")
		}
		if !a.HasPermission(userID, models.ResourceUsers, models.ActionWrite, orgID) {
			return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Insufficient permissions", nil, "")
		}
	}

	now := time.Now()
	if err := a.DB.Model(&device).Updates(map[string]any{
		"revoked_at":           now,
		"revoked_by":           userID,
		"push_token":           "",
		"previous_token_hash":  nil,
		"previous_valid_until": nil,
	}).Error; err != nil {
		a.Log.Error("Failed to remove device", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to remove device", nil, "")
	}

	a.logAudit(device.OrganizationID, userID, "mobile_device", device.ID, models.AuditActionDeleted,
		map[string]any{"name": device.Name, "user_id": device.UserID.String()}, nil)
	// Close the person's live connections; their other phones and browsers
	// reconnect on their own, this one can't.
	if a.WSHub != nil {
		a.WSHub.DisconnectUser(device.UserID)
	}

	return r.SendEnvelope(map[string]string{"message": "Device signed out"})
}

// UpdateMobilePushToken stores the push token of the phone making the request.
func (a *App) UpdateMobilePushToken(r *fastglue.Request) error {
	deviceID, ok := middleware.GetDeviceID(r)
	if !ok {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Only a signed-in phone can set a push token", nil, "")
	}
	var req MobilePushTokenRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	if err := a.DB.Model(&models.MobileDevice{}).Where("id = ?", deviceID).
		Update("push_token", clip(req.PushToken, 4096)).Error; err != nil {
		a.Log.Error("Failed to store push token", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to store push token", nil, "")
	}
	return r.SendEnvelope(map[string]string{"status": "ok"})
}

// revokeMobileDevices signs out a user's phones, in one organization or, with
// uuid.Nil, everywhere.
func (a *App) revokeMobileDevices(userID, orgID, byUserID uuid.UUID) {
	query := a.DB.Model(&models.MobileDevice{}).Where("user_id = ? AND revoked_at IS NULL", userID)
	if orgID != uuid.Nil {
		query = query.Where("organization_id = ?", orgID)
	}
	if err := query.Updates(map[string]any{
		"revoked_at": time.Now(),
		"revoked_by": byUserID,
		"push_token": "",
	}).Error; err != nil {
		a.Log.Error("Failed to sign out user's devices", "error", err, "user_id", userID)
	}
	// Live connections stay open after a token is revoked; close them so a
	// removed phone stops receiving messages straight away.
	if a.WSHub != nil {
		a.WSHub.DisconnectUser(userID)
	}
}

// mobileDeviceUser loads the device's user in the device's organization, with
// the role that applies there. A non-empty reason means the phone must be
// signed out.
func (a *App) mobileDeviceUser(tx *gorm.DB, device *models.MobileDevice) (*models.User, string, error) {
	var user models.User
	if err := tx.Where("id = ?", device.UserID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "User not found", nil
		}
		return nil, "", err
	}
	if !user.IsActive {
		return nil, "Account is disabled", nil
	}

	if device.OrganizationID != user.OrganizationID {
		if user.IsSuperAdmin {
			var count int64
			if err := tx.Table("organizations").Where("id = ? AND deleted_at IS NULL", device.OrganizationID).Count(&count).Error; err != nil {
				return nil, "", err
			}
			if count == 0 {
				return nil, "Organization not found", nil
			}
		} else {
			var userOrg models.UserOrganization
			if err := tx.Where("user_id = ? AND organization_id = ?", user.ID, device.OrganizationID).First(&userOrg).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, "No longer a member of this organization", nil
				}
				return nil, "", err
			}
			user.RoleID = userOrg.RoleID
		}
		user.OrganizationID = device.OrganizationID
	}
	return &user, "", nil
}

// sendMobileTokens answers a sign-in or refresh with the tokens in the body.
func (a *App) sendMobileTokens(r *fastglue.Request, user *models.User, device *models.MobileDevice, refreshToken string) error {
	accessToken, err := a.signAccessToken(user, &device.ID)
	if err != nil {
		a.Log.Error("Failed to generate access token", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to generate token", nil, "")
	}

	if user.RoleID != nil {
		var role models.CustomRole
		if err := a.DB.Where("id = ?", *user.RoleID).First(&role).Error; err == nil {
			if cachedPerms, err := a.GetRolePermissionsCached(*user.RoleID); err == nil {
				permissions := make([]models.Permission, 0, len(cachedPerms))
				for _, p := range cachedPerms {
					if parts := splitPermission(p); len(parts) == 2 {
						permissions = append(permissions, models.Permission{Resource: parts[0], Action: parts[1]})
					}
				}
				role.Permissions = permissions
			}
			user.Role = &role
		}
	}

	return r.SendEnvelope(MobileAuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    a.Config.JWT.AccessExpiryMins * 60,
		DeviceID:     device.ID,
		Device:       mobileDeviceToResponse(device, device.ID),
		User:         *user,
	})
}

func mobileDeviceToResponse(d *models.MobileDevice, current uuid.UUID) MobileDeviceResponse {
	resp := MobileDeviceResponse{
		ID:          d.ID,
		UserID:      d.UserID,
		Name:        d.Name,
		Model:       d.Model,
		Platform:    d.Platform,
		AppVersion:  d.AppVersion,
		PushEnabled: d.PushToken != "",
		Rings:       d.Rings,
		Current:     d.ID == current,
		CreatedAt:   d.CreatedAt,
		LastSeenAt:  d.LastSeenAt,
	}
	if d.User != nil {
		resp.UserName = d.User.FullName
		resp.UserEmail = d.User.Email
	}
	return resp
}

// newMobileRefreshToken returns a random device refresh token and the hash
// stored for it. The token has 256 bits of entropy, so a plain SHA-256 is
// enough and keeps the lookup indexable.
func newMobileRefreshToken() (token, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = mobileRefreshTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return token, hashMobileToken(token), nil
}

func hashMobileToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// clip trims s and cuts it to at most n bytes without splitting a character.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
