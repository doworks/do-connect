package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Linking users to clinic system logins. The patient panel acts in the clinic
// system as the staff login with the user's email; when the two systems use
// different emails, an admin links the user to the right staff login here.

// ClinicStaff is a staff login in the clinic system.
type ClinicStaff struct {
	Email     string   `json:"email"`
	FullName  string   `json:"full_name"`
	Roles     []string `json:"roles"`
	MoreRoles int      `json:"more_roles"`
}

// ClinicLink says which staff login a user acts as, and whether it exists.
type ClinicLink struct {
	UserID     uuid.UUID `json:"user_id"`
	ClinicUser string    `json:"clinic_user"`
	ActsAs     string    `json:"acts_as"`
	Linked     bool      `json:"linked"`
	StaffName  string    `json:"staff_name,omitempty"`
}

// clinicLogin is the clinic system login a user acts as.
func clinicLogin(user models.User) string {
	if user.ClinicUser != "" {
		return user.ClinicUser
	}
	return user.Email
}

// clinicStaff asks the clinic system for staff logins, not on anyone's behalf.
func (a *App) clinicStaff(args map[string]any) ([]ClinicStaff, int, string) {
	status, data, message := a.callClinic(a.Config.Clinic, clinicRequest{
		Action:    "staff_users",
		Args:      args,
		Timestamp: time.Now().Unix(),
	})
	if status != fasthttp.StatusOK {
		return nil, status, message
	}
	var out struct {
		Users []ClinicStaff `json:"users"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fasthttp.StatusBadGateway, "The clinic system's answer wasn't understood"
	}
	return out.Users, fasthttp.StatusOK, ""
}

func (a *App) requireClinic(r *fastglue.Request) bool {
	if a.Config == nil || !a.Config.Clinic.Enabled() {
		_ = r.SendErrorEnvelope(fasthttp.StatusNotFound, "The patient panel isn't set up", nil, "")
		return false
	}
	return true
}

// SearchClinicStaff lists clinic system staff logins to link a user to.
func (a *App) SearchClinicStaff(r *fastglue.Request) error {
	if !a.requireClinic(r) {
		return nil
	}
	if _, _, err := a.requireAuth(r, models.ResourceUsers, models.ActionWrite); err != nil {
		return nil
	}
	staff, status, message := a.clinicStaff(map[string]any{"query": strings.TrimSpace(string(r.RequestCtx.QueryArgs().Peek("search")))})
	if status != fasthttp.StatusOK {
		return r.SendErrorEnvelope(status, message, nil, "")
	}
	return r.SendEnvelope(map[string]any{"users": staff})
}

// ListClinicLinks says, for every user in the organisation, which staff login
// they act as and whether the clinic system knows it.
func (a *App) ListClinicLinks(r *fastglue.Request) error {
	if !a.requireClinic(r) {
		return nil
	}
	orgID, _, err := a.requireAuth(r, models.ResourceUsers, models.ActionRead)
	if err != nil {
		return nil
	}
	var users []models.User
	if err := a.DB.Select("users.id", "users.email", "users.clinic_user").
		Joins("JOIN user_organizations ON user_organizations.user_id = users.id AND user_organizations.organization_id = ? AND user_organizations.deleted_at IS NULL", orgID).
		Where("users.deleted_at IS NULL").Find(&users).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list users", nil, "")
	}
	emails := make([]string, 0, len(users))
	for _, u := range users {
		emails = append(emails, clinicLogin(u))
	}
	staff, status, message := a.clinicStaff(map[string]any{"emails": emails})
	if status != fasthttp.StatusOK {
		return r.SendErrorEnvelope(status, message, nil, "")
	}
	known := make(map[string]string, len(staff))
	for _, s := range staff {
		known[strings.ToLower(s.Email)] = s.FullName
	}
	links := make([]ClinicLink, 0, len(users))
	for _, u := range users {
		name, ok := known[strings.ToLower(clinicLogin(u))]
		links = append(links, ClinicLink{UserID: u.ID, ClinicUser: u.ClinicUser, ActsAs: clinicLogin(u), Linked: ok, StaffName: name})
	}
	return r.SendEnvelope(map[string]any{"links": links})
}

// SetClinicUser links a user to a clinic system staff login, or back to their
// own email when clinic_user is empty.
func (a *App) SetClinicUser(r *fastglue.Request) error {
	if !a.requireClinic(r) {
		return nil
	}
	orgID, currentUserID, err := a.requireAuth(r, models.ResourceUsers, models.ActionWrite)
	if err != nil {
		return nil
	}
	id, err := parsePathUUID(r, "id", "user")
	if err != nil {
		return nil
	}
	var req struct {
		ClinicUser string `json:"clinic_user"`
	}
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	req.ClinicUser = strings.TrimSpace(req.ClinicUser)

	var user models.User
	if err := a.DB.Select("users.*").
		Joins("JOIN user_organizations ON user_organizations.user_id = users.id AND user_organizations.organization_id = ? AND user_organizations.deleted_at IS NULL", orgID).
		Where("users.id = ? AND users.deleted_at IS NULL", id).First(&user).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "User not found", nil, "")
	}

	staffName := ""
	if req.ClinicUser != "" {
		staff, status, message := a.clinicStaff(map[string]any{"emails": []string{req.ClinicUser}})
		if status != fasthttp.StatusOK {
			return r.SendErrorEnvelope(status, message, nil, "")
		}
		if len(staff) == 0 {
			return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity, "No active staff login in the clinic system uses "+req.ClinicUser, nil, "")
		}
		req.ClinicUser = staff[0].Email
		staffName = staff[0].FullName
		if strings.EqualFold(req.ClinicUser, user.Email) {
			req.ClinicUser = "" // the same as their own email: no link needed
		}
	}

	old := map[string]any{"clinic_user": user.ClinicUser}
	if err := a.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("clinic_user", req.ClinicUser).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to save the link", nil, "")
	}
	a.forgetClinicPhotos(user.ID)
	a.logAudit(orgID, currentUserID, "user", user.ID, models.AuditActionUpdated, old, map[string]any{"clinic_user": req.ClinicUser})
	user.ClinicUser = req.ClinicUser
	link := ClinicLink{UserID: user.ID, ClinicUser: user.ClinicUser, ActsAs: clinicLogin(user), Linked: staffName != "", StaffName: staffName}
	if user.ClinicUser == "" {
		// Back on their own email: say whether the clinic system knows it.
		if staff, status, _ := a.clinicStaff(map[string]any{"emails": []string{user.Email}}); status == fasthttp.StatusOK && len(staff) > 0 {
			link.Linked, link.StaffName = true, staff[0].FullName
		}
	}
	return r.SendEnvelope(link)
}

// forgetClinicPhotos drops whose-photo answers cached for a user, which
// depended on the staff login they acted as.
func (a *App) forgetClinicPhotos(userID uuid.UUID) {
	if a.Redis == nil {
		return
	}
	ctx := context.Background()
	iter := a.Redis.Scan(ctx, 0, fmt.Sprintf("clinic:photo:who:%s:*", userID), 200).Iterator()
	for iter.Next(ctx) {
		a.Redis.Del(ctx, iter.Val())
	}
}
