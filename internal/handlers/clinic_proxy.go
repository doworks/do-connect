package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// The patient panel asks the clinic system, never its database. Each request
// names the staff member by email and is signed with the shared secret, so the
// clinic system can act as that person and apply their own permissions.

const (
	clinicDefaultTimeout   = 20 * time.Second
	clinicMaxResponseBytes = 2 << 20
)

// clinicActions lists what the panel may ask for: the HTTP method the screen
// uses, the path after /api/clinic/, and the action name the clinic system sees.
var clinicActions = map[string]string{
	"GET context":           "context",
	"GET booking-options":   "booking_options",
	"GET slots":             "slots",
	"GET find-patients":     "find_patients",
	"POST book":             "book",
	"POST reschedule":       "reschedule",
	"POST confirm":          "confirm",
	"POST cancel":           "cancel",
	"POST send-document":    "send_document",
	"POST send-health-form": "send_health_form",
	"POST patients":         "create_patient",
	"POST link-number":      "link_number",
}

type clinicRequest struct {
	Action     string         `json:"action"`
	Args       map[string]any `json:"args"`
	ActingUser string         `json:"acting_user"`
	Timestamp  int64          `json:"timestamp"`
}

// signClinicRequest is what the clinic system checks: HMAC-SHA256 of
// "<timestamp>.<body>" with the shared secret, in hex.
func signClinicRequest(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ClinicProxy forwards one panel request to the clinic system.
func (a *App) ClinicProxy(r *fastglue.Request) error {
	cfg := a.Config.Clinic
	if !cfg.Enabled() {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "The patient panel isn't set up", nil, "")
	}
	orgID, userID, err := a.requireAuth(r, models.ResourceContacts, models.ActionRead)
	if err != nil {
		return nil
	}
	if viaKey, _ := r.RequestCtx.UserValue(middleware.ContextKeyViaAPIKey).(bool); viaKey {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "The patient panel is for signed-in staff", nil, "")
	}

	path, _ := r.RequestCtx.UserValue("action").(string)
	action, ok := clinicActions[string(r.RequestCtx.Method())+" "+path]
	if !ok {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Unknown panel request", nil, "")
	}

	var user models.User
	if err := a.DB.Select("id", "email").Where("id = ? AND organization_id = ?", userID, orgID).First(&user).Error; err != nil || user.Email == "" {
		// Super admins can belong to several organisations; fall back to the user alone.
		if err := a.DB.Select("id", "email").Where("id = ?", userID).First(&user).Error; err != nil || user.Email == "" {
			return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Your account has no email to match in the clinic system", nil, "")
		}
	}

	args := map[string]any{}
	if r.RequestCtx.IsGet() {
		r.RequestCtx.QueryArgs().VisitAll(func(key, value []byte) {
			args[string(key)] = string(value)
		})
	} else if body := r.RequestCtx.PostBody(); len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &args); err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
		}
	}

	status, data, message := a.callClinic(cfg, clinicRequest{
		Action:     action,
		Args:       args,
		ActingUser: user.Email,
		Timestamp:  time.Now().Unix(),
	})
	if status != fasthttp.StatusOK {
		return r.SendErrorEnvelope(status, message, nil, "")
	}
	return r.SendEnvelope(data)
}

// callClinic returns the status to give the screen, the clinic system's answer,
// and a readable message when it failed.
func (a *App) callClinic(cfg config.ClinicConfig, req clinicRequest) (int, json.RawMessage, string) {
	body, err := json.Marshal(req)
	if err != nil {
		return fasthttp.StatusInternalServerError, nil, "Could not prepare the request"
	}
	timeout := clinicDefaultTimeout
	if cfg.TimeoutSecs > 0 {
		timeout = time.Duration(cfg.TimeoutSecs) * time.Second
	}
	url := strings.TrimRight(cfg.URL, "/") + "/api/method/" + cfg.Method
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fasthttp.StatusInternalServerError, nil, "The clinic address in the settings is invalid"
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Whatomate-Timestamp", strconv.FormatInt(req.Timestamp, 10))
	httpReq.Header.Set("X-Whatomate-Signature", signClinicRequest(cfg.Secret, req.Timestamp, body))

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		a.Log.Warn("Clinic system unreachable", "action", req.Action, "error", err)
		return fasthttp.StatusBadGateway, nil, "The clinic system didn't answer. Try again in a moment."
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, clinicMaxResponseBytes))
	if err != nil {
		return fasthttp.StatusBadGateway, nil, "The clinic system's answer was cut off"
	}

	var parsed struct {
		Message        json.RawMessage `json:"message"`
		ServerMessages string          `json:"_server_messages"`
		ExcType        string          `json:"exc_type"`
	}
	_ = json.Unmarshal(raw, &parsed)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if len(parsed.Message) == 0 {
			return fasthttp.StatusOK, json.RawMessage("null"), ""
		}
		return fasthttp.StatusOK, parsed.Message, ""
	}

	message := clinicErrorMessage(parsed.ServerMessages)
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		if message == "" {
			message = "The clinic system says you're not allowed to do this"
		}
		return fasthttp.StatusForbidden, nil, message
	case resp.StatusCode == http.StatusNotFound:
		if message == "" {
			message = "Not found in the clinic system"
		}
		return fasthttp.StatusNotFound, nil, message
	case resp.StatusCode < 500 && message != "":
		// The clinic system refused with a reason for staff, e.g. a clash.
		return fasthttp.StatusUnprocessableEntity, nil, message
	}
	a.Log.Warn("Clinic system error", "action", req.Action, "status", resp.StatusCode, "exc_type", parsed.ExcType)
	if message == "" {
		message = fmt.Sprintf("The clinic system had a problem (%d)", resp.StatusCode)
	}
	return fasthttp.StatusBadGateway, nil, message
}

var htmlTags = regexp.MustCompile(`<[^>]*>`)

// clinicErrorMessage pulls the text staff should see out of Frappe's
// _server_messages, a JSON list of JSON-encoded {"message": ...} objects.
func clinicErrorMessage(serverMessages string) string {
	if serverMessages == "" {
		return ""
	}
	var list []string
	if err := json.Unmarshal([]byte(serverMessages), &list); err != nil {
		return ""
	}
	var parts []string
	for _, item := range list {
		var m struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(item), &m) == nil && m.Message != "" {
			parts = append(parts, strings.TrimSpace(htmlTags.ReplaceAllString(m.Message, "")))
		}
	}
	return strings.Join(parts, " ")
}
