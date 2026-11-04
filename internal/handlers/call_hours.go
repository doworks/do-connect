package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/audit"
	"github.com/shridarpatil/whatomate/internal/callhours"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/templateutil"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Calling hours: when the clinic takes calls, who rings, and what a patient is
// told when nobody can answer. The rules themselves live in internal/callhours.

// GetCallRules handles GET /api/calling/rules
func (a *App) GetCallRules(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceSettingsGeneral, models.ActionRead)
	if err != nil {
		return nil
	}
	rules, err := callhours.Load(a.DB, orgID)
	if err != nil {
		a.Log.Error("Failed to load calling hours", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Could not load calling hours", nil, "")
	}
	return r.SendEnvelope(rules)
}

// UpdateCallRules handles PUT /api/calling/rules
func (a *App) UpdateCallRules(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSettingsGeneral, models.ActionWrite)
	if err != nil {
		return nil
	}
	var req callhours.Rules
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	before, err := callhours.Load(a.DB, orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Could not load calling hours", nil, "")
	}
	if err := callhours.Save(a.DB, orgID, req); err != nil {
		var invalid callhours.ValidationError
		if errors.As(err, &invalid) {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, invalid.Error(), nil, "")
		}
		a.Log.Error("Failed to save calling hours", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Could not save calling hours", nil, "")
	}
	if a.CallManager != nil {
		a.CallManager.InvalidateOrgCallingSettingsCache(orgID)
	}

	after, err := callhours.Load(a.DB, orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Could not load calling hours", nil, "")
	}
	audit.LogAudit(a.DB, orgID, userID, audit.GetUserName(a.DB, userID),
		models.ResourceSettingsCalling, orgID, models.AuditActionUpdated, before, after)
	return r.SendEnvelope(after)
}

// callScreen is what the calling hours make of a call arriving now.
type callScreen struct {
	rules *callhours.Rules // nil when the organization hasn't set calling hours
	// decline means nobody may take the call: it is declined and the patient
	// gets the closed message.
	decline bool
	// ring is who may be rung. Only meaningful when rules is set.
	ring []uuid.UUID
}

// screenCall applies the calling hours to an incoming call. A click-to-call call
// was asked for by one person minutes ago, so only "close for everyone" stops
// it; whether that person can take it is checked by the sticky route.
func (a *App) screenCall(orgID uuid.UUID, sticky bool, at time.Time) callScreen {
	rules, err := callhours.Load(a.DB, orgID)
	if err != nil {
		a.Log.Error("Failed to load calling hours; the call is handled as usual", "error", err, "org_id", orgID)
		return callScreen{}
	}
	if !rules.Configured {
		return callScreen{}
	}
	screen := callScreen{rules: rules}
	for _, id := range rules.RingingStaff(at) {
		if parsed, err := uuid.Parse(id); err == nil {
			screen.ring = append(screen.ring, parsed)
		}
	}
	switch {
	case rules.ClosedNow:
		screen.decline = true
	case sticky:
	case !rules.IsOpen(at) && rules.OutsideAction != callhours.OutsideRing:
		// Outside hours the call only goes through to on-call staff who are
		// signed in; otherwise it would wait for nobody. In hours an empty
		// line goes the usual way and ends as a missed call.
		online := screen.ring
		if a.WSHub != nil {
			online = a.WSHub.FilterOnlineUsers(orgID, screen.ring)
		}
		screen.decline = len(online) == 0
	}
	return screen
}

// callTurnedAway reports whether the server already declined this call, so the
// events Meta sends after the first one leave it alone.
func callTurnedAway(callLog *models.CallLog) bool {
	return callLog.Status == models.CallStatusMissed && callLog.DisconnectedBy == models.DisconnectedBySystem && callLog.AnsweredAt == nil
}

// declineClosedCall turns a call away outside calling hours: Meta stops
// ringing, the patient is told when the clinic opens, and the chat shows the
// missed call under Waiting for staff to pick up later.
func (a *App) declineClosedCall(account *models.WhatsAppAccount, contact *models.Contact, callLog *models.CallLog, rules *callhours.Rules, now time.Time) {
	if a.CallManager != nil {
		a.CallManager.RejectIncomingCall(context.Background(), account, callLog.WhatsAppCallID)
	}
	a.DB.Model(callLog).Updates(map[string]any{
		"status":          models.CallStatusMissed,
		"ended_at":        now,
		"disconnected_by": models.DisconnectedBySystem,
	})
	callLog.Status = models.CallStatusMissed
	callLog.DisconnectedBy = models.DisconnectedBySystem

	a.Log.Info("Declined call outside calling hours", "call_id", callLog.WhatsAppCallID, "closed_now", rules.ClosedNow)

	a.sendCallNotice(account, contact, rules, now)
	a.recordMissedCallMessage(callLog, contact)

	a.broadcastCallEvent(account.OrganizationID, websocket.TypeCallEnded, map[string]any{
		"call_id":         callLog.WhatsAppCallID,
		"contact_id":      contact.ID.String(),
		"status":          string(models.CallStatusMissed),
		"duration":        0,
		"ended_at":        now.Format(time.RFC3339),
		"disconnected_by": string(models.DisconnectedBySystem),
	})
}

// noticeAfterMissedCall tells the patient about a call nobody answered, using
// the organization's calling hours. Nothing is sent before they're set.
func (a *App) noticeAfterMissedCall(account *models.WhatsAppAccount, contact *models.Contact, now time.Time) {
	rules, err := callhours.Load(a.DB, account.OrganizationID)
	if err != nil || !rules.Configured {
		return
	}
	a.sendCallNotice(account, contact, rules, now)
}

// callNoticeKey marks the messages sent about a call, for the gap between them.
const callNoticeKey = "call_notice"

// sendCallNotice sends the closed message, or the missed call one while the
// clinic is open, in the patient's language. Inside the 24-hour window it is the
// typed text; outside it, the template for that language. A number gets at most
// one in the configured gap.
func (a *App) sendCallNotice(account *models.WhatsAppAccount, contact *models.Contact, rules *callhours.Rules, now time.Time) {
	kind, texts, templates := "missed", rules.Messages.Missed, rules.Templates.Missed
	if !rules.IsOpen(now) && rules.OutsideAction != callhours.OutsideRing {
		kind, texts, templates = "closed", rules.Messages.Closed, rules.Templates.Closed
	}

	if rules.MessageGapHours > 0 {
		var recent int64
		a.DB.Model(&models.Message{}).
			Where("contact_id = ? AND direction = ? AND metadata->>'"+callNoticeKey+"' IS NOT NULL AND status <> ? AND created_at > ?",
				contact.ID, models.DirectionOutgoing, models.MessageStatusFailed,
				now.Add(-time.Duration(rules.MessageGapHours*float64(time.Hour)))).
			Count(&recent)
		if recent > 0 {
			return
		}
	}

	// With no Arabic text the whole message is English: one message is never mixed.
	language, text := "en", strings.TrimSpace(texts.EN)
	if contactPrefersArabic(contact) && strings.TrimSpace(texts.AR) != "" {
		language, text = "ar", strings.TrimSpace(texts.AR)
	}

	opts := DefaultSendOptions()
	opts.SentByLabel = "Automatic"
	opts.Metadata = models.JSONB{callNoticeKey: kind}

	if text != "" && serviceWindowOpen(contact) {
		text = strings.ReplaceAll(text, "{next_open}", rules.NextOpenLabel(now, language))
		if _, err := a.SendOutgoingMessage(context.Background(), OutgoingMessageRequest{
			Account: account, Contact: contact, Type: models.MessageTypeText, Content: text,
		}, opts); err != nil {
			a.Log.Error("Failed to send the call message", "error", err, "contact_id", contact.ID)
		}
		return
	}

	template, language := a.callNoticeTemplate(account, contact, templates)
	if template == nil {
		a.Log.Warn("No call message sent: the patient is outside the 24-hour window and no approved template is set",
			"contact_id", contact.ID, "kind", kind)
		return
	}
	// The template's variables, if any, take the next opening time.
	params := map[string]string{}
	for _, name := range templateutil.ExtParamNames(template.BodyContent) {
		params[name] = rules.NextOpenLabel(now, language)
	}
	if _, err := a.SendOutgoingMessage(context.Background(), OutgoingMessageRequest{
		Account: account, Contact: contact, Type: models.MessageTypeTemplate, Template: template, BodyParams: params,
	}, opts); err != nil {
		a.Log.Error("Failed to send the call template", "error", err, "contact_id", contact.ID)
	}
}

// callNoticeTemplate finds the approved template in the patient's language,
// falling back to English.
func (a *App) callNoticeTemplate(account *models.WhatsAppAccount, contact *models.Contact, names callhours.ByLanguage) (*models.Template, string) {
	type choice struct{ name, language string }
	choices := []choice{{names.EN, "en"}}
	if contactPrefersArabic(contact) {
		choices = []choice{{names.AR, "ar"}, {names.EN, "en"}}
	}
	for _, option := range choices {
		if option.name == "" {
			continue
		}
		var template models.Template
		err := a.DB.Where("organization_id = ? AND whats_app_account = ? AND name = ? AND status = ?",
			account.OrganizationID, account.Name, option.name, "APPROVED").First(&template).Error
		if err == nil {
			return &template, option.language
		}
	}
	return nil, "en"
}
