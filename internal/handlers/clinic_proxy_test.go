package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

const testClinicSecret = "a-shared-secret-of-at-least-32-characters"

type fakeClinic struct {
	mu       sync.Mutex
	requests []clinicRequest
	valid    []bool
	reply    func(req clinicRequest) (int, string)
}

func newFakeClinic(t *testing.T) (*fakeClinic, *httptest.Server) {
	t.Helper()
	fc := &fakeClinic{reply: func(clinicRequest) (int, string) { return 200, `{"message": {"ok": true}}` }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-Whatomate-Timestamp"), 10, 64)
		var req clinicRequest
		_ = json.Unmarshal(body, &req)
		fc.mu.Lock()
		fc.requests = append(fc.requests, req)
		fc.valid = append(fc.valid, r.URL.Path == "/api/method/clinic.panel.call" &&
			r.Header.Get("X-Whatomate-Signature") == signClinicRequest(testClinicSecret, ts, body) && ts == req.Timestamp)
		fc.mu.Unlock()
		status, out := fc.reply(req)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	return fc, srv
}

func (f heldFixture) clinic(url string) {
	f.app.Config.Clinic = config.ClinicConfig{URL: url, Method: "clinic.panel.call", Secret: testClinicSecret}
}

func (f heldFixture) panel(t *testing.T, method, action string, payload map[string]any, query map[string]string) *fastglue.Request {
	t.Helper()
	var req *fastglue.Request
	if method == "GET" {
		req = testutil.NewGETRequest(t)
		for k, v := range query {
			testutil.SetQueryParam(req, k, v)
		}
	} else {
		req = testutil.NewJSONRequest(t, payload)
	}
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	testutil.SetPathParam(req, "action", action)
	require.NoError(t, f.app.ClinicProxy(req))
	return req
}

func TestClinicPanel(t *testing.T) {
	t.Run("off until it's set up", func(t *testing.T) {
		f := newHeldFixture(t)
		req := f.panel(t, "GET", "context", nil, map[string]string{"phone": "97333112233"})
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))

		f.app.Config.Clinic = config.ClinicConfig{URL: "https://clinic.test", Method: "clinic.panel.call", Secret: "too-short"}
		req = f.panel(t, "GET", "context", nil, map[string]string{"phone": "97333112233"})
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req), "a weak secret leaves it off")
	})

	t.Run("signs the request and names the staff member", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)
		fc.reply = func(clinicRequest) (int, string) {
			return 200, `{"message": {"phone": "97333112233", "patients": [{"name": "PAT-1"}]}}`
		}

		req := f.panel(t, "GET", "context", nil, map[string]string{"phone": "97333112233"})
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
		var out struct {
			Data struct {
				Patients []struct{ Name string } `json:"patients"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &out))
		assert.Equal(t, "PAT-1", out.Data.Patients[0].Name)

		require.Len(t, fc.requests, 1)
		assert.True(t, fc.valid[0], "path and signature match what the clinic system checks")
		assert.Equal(t, "context", fc.requests[0].Action)
		assert.Equal(t, f.user.Email, fc.requests[0].ActingUser)
		assert.Equal(t, "97333112233", fc.requests[0].Args["phone"])
		assert.InDelta(t, time.Now().Unix(), fc.requests[0].Timestamp, 5)
	})

	t.Run("posts body arguments under the clinic's action name", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)
		req := f.panel(t, "POST", "patients", map[string]any{"first_name": "Huda", "phone": "97333112233"}, nil)
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
		assert.Equal(t, "create_patient", fc.requests[0].Action)
		assert.Equal(t, "Huda", fc.requests[0].Args["first_name"])
	})

	t.Run("only known requests, and only from signed-in staff", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)

		req := f.panel(t, "POST", "context", map[string]any{}, nil)
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req), "context is GET only")
		req = f.panel(t, "GET", "delete_everything", nil, nil)
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))

		req = testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		testutil.SetPathParam(req, "action", "context")
		req.RequestCtx.SetUserValue(middleware.ContextKeyViaAPIKey, true)
		require.NoError(t, f.app.ClinicProxy(req))
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
		assert.Empty(t, fc.requests, "nothing reached the clinic system")
	})

	t.Run("passes on the clinic system's reasons", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)

		fc.reply = func(clinicRequest) (int, string) {
			return 417, `{"exc_type": "ValidationError", "_server_messages": "[\"{\\\"message\\\": \\\"Dr Amal already has a visit at <b>5:00 PM</b>\\\"}\"]"}`
		}
		req := f.panel(t, "POST", "book", map[string]any{"patient": "PAT-1"}, nil)
		assert.Equal(t, fasthttp.StatusUnprocessableEntity, testutil.GetResponseStatusCode(req))
		assert.Contains(t, string(testutil.GetResponseBody(req)), "Dr Amal already has a visit at 5:00 PM")

		fc.reply = func(clinicRequest) (int, string) { return 403, `{"exc_type": "PermissionError"}` }
		req = f.panel(t, "POST", "cancel", map[string]any{"appointment": "APP-1"}, nil)
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))

		fc.reply = func(clinicRequest) (int, string) { return 500, `<html>Internal Server Error</html>` }
		req = f.panel(t, "GET", "slots", nil, map[string]string{"practitioner": "DR-1"})
		assert.Equal(t, fasthttp.StatusBadGateway, testutil.GetResponseStatusCode(req))

		srv.Close()
		req = f.panel(t, "GET", "context", nil, map[string]string{"phone": "1"})
		assert.Equal(t, fasthttp.StatusBadGateway, testutil.GetResponseStatusCode(req), "clinic system down")
	})
}
