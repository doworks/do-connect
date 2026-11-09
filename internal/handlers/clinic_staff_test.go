package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// staffClinic answers like the clinic system: two staff logins.
func staffClinic(req clinicRequest) (int, string) {
	all := []ClinicStaff{
		{Email: "noor@clinic.test", FullName: "Noor Reception", Roles: []string{"Receptionist"}},
		{Email: "sara@clinic.test", FullName: "Sara Nurse", Roles: []string{"Nursing User"}},
	}
	var out []ClinicStaff
	if emails, ok := req.Args["emails"].([]any); ok {
		for _, e := range emails {
			for _, s := range all {
				if strings.EqualFold(s.Email, e.(string)) {
					out = append(out, s)
				}
			}
		}
	} else {
		q, _ := req.Args["query"].(string)
		for _, s := range all {
			if strings.Contains(strings.ToLower(s.FullName+s.Email), strings.ToLower(q)) {
				out = append(out, s)
			}
		}
	}
	body, _ := json.Marshal(map[string]any{"message": map[string]any{"users": out}})
	return 200, string(body)
}

func TestClinicStaffLinks(t *testing.T) {
	f := newHeldFixture(t)
	fc, srv := newFakeClinic(t)
	f.clinic(srv.URL)
	fc.reply = staffClinic
	desk := testutil.CreateTestUser(t, f.app.DB, f.org.ID, testutil.WithRoleID(f.user.RoleID), testutil.WithEmail("desk-"+uuid.NewString()[:6]+"@do-connect.test"))
	asAdmin := func(req *fastglue.Request) *fastglue.Request {
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		return req
	}
	data := func(t *testing.T, req *fastglue.Request, into any) {
		t.Helper()
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req)))
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &env))
		require.NoError(t, json.Unmarshal(env.Data, into))
	}
	link := func(t *testing.T, who uuid.UUID, clinicUser string) *fastglue.Request {
		req := asAdmin(testutil.NewJSONRequest(t, map[string]any{"clinic_user": clinicUser}))
		testutil.SetPathParam(req, "id", who.String())
		require.NoError(t, f.app.SetClinicUser(req))
		return req
	}
	links := func(t *testing.T) map[uuid.UUID]ClinicLink {
		req := asAdmin(testutil.NewGETRequest(t))
		require.NoError(t, f.app.ListClinicLinks(req))
		var out struct {
			Links []ClinicLink `json:"links"`
		}
		data(t, req, &out)
		m := map[uuid.UUID]ClinicLink{}
		for _, l := range out.Links {
			m[l.UserID] = l
		}
		return m
	}

	t.Run("staff logins are searched without acting as anyone", func(t *testing.T) {
		req := asAdmin(testutil.NewGETRequest(t))
		testutil.SetQueryParam(req, "search", "noor")
		require.NoError(t, f.app.SearchClinicStaff(req))
		var out struct {
			Users []ClinicStaff `json:"users"`
		}
		data(t, req, &out)
		require.Len(t, out.Users, 1)
		assert.Equal(t, "noor@clinic.test", out.Users[0].Email)
		fc.mu.Lock()
		last := fc.requests[len(fc.requests)-1]
		valid := fc.valid[len(fc.valid)-1]
		fc.mu.Unlock()
		assert.Equal(t, "staff_users", last.Action)
		assert.Empty(t, last.ActingUser)
		assert.True(t, valid, "signed like every clinic request")
	})

	t.Run("unlinked users show as unlinked, until linked", func(t *testing.T) {
		before := links(t)[desk.ID]
		assert.False(t, before.Linked)
		assert.Equal(t, desk.Email, before.ActsAs)

		var saved ClinicLink
		data(t, link(t, desk.ID, " NOOR@clinic.test "), &saved)
		assert.True(t, saved.Linked)
		assert.Equal(t, "noor@clinic.test", saved.ClinicUser, "stored as the clinic system spells it")
		assert.Equal(t, "Noor Reception", saved.StaffName)

		after := links(t)[desk.ID]
		assert.True(t, after.Linked)
		assert.Equal(t, "noor@clinic.test", after.ActsAs)
	})

	t.Run("the panel then acts as the linked staff login", func(t *testing.T) {
		fc.reply = func(req clinicRequest) (int, string) {
			if req.Action == "staff_users" {
				return staffClinic(req)
			}
			return 200, `{"message": {"ok": true}}`
		}
		req := testutil.NewGETRequest(t)
		testutil.SetQueryParam(req, "phone", "97333000111")
		testutil.SetAuthContext(req, f.org.ID, desk.ID)
		testutil.SetPathParam(req, "action", "context")
		require.NoError(t, f.app.ClinicProxy(req))
		fc.mu.Lock()
		last := fc.requests[len(fc.requests)-1]
		fc.mu.Unlock()
		assert.Equal(t, "context", last.Action)
		assert.Equal(t, "noor@clinic.test", last.ActingUser)
		fc.reply = staffClinic
	})

	t.Run("a login the clinic system doesn't have is refused", func(t *testing.T) {
		assert.Equal(t, fasthttp.StatusUnprocessableEntity, testutil.GetResponseStatusCode(link(t, desk.ID, "ghost@clinic.test")))
		assert.Equal(t, "noor@clinic.test", links(t)[desk.ID].ActsAs, "unchanged")
	})

	t.Run("an empty link goes back to their own email", func(t *testing.T) {
		var saved ClinicLink
		data(t, link(t, desk.ID, ""), &saved)
		assert.Empty(t, saved.ClinicUser)
		assert.Equal(t, desk.Email, saved.ActsAs)
		assert.False(t, saved.Linked)
		var u models.User
		require.NoError(t, f.app.DB.First(&u, "id = ?", desk.ID).Error)
		assert.Empty(t, u.ClinicUser)
	})

	t.Run("only people who may edit users can link, and not themselves without it", func(t *testing.T) {
		role := testutil.CreateTestRoleWithKeys(t, f.app.DB, f.org.ID, "agent-"+uuid.NewString()[:6], []string{"chat:read", "contacts:read"})
		agent := testutil.CreateTestUser(t, f.app.DB, f.org.ID, testutil.WithRoleID(&role.ID))
		req := testutil.NewJSONRequest(t, map[string]any{"clinic_user": "sara@clinic.test"})
		testutil.SetAuthContext(req, f.org.ID, agent.ID)
		testutil.SetPathParam(req, "id", agent.ID.String())
		require.NoError(t, f.app.SetClinicUser(req))
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))

		search := testutil.NewGETRequest(t)
		testutil.SetAuthContext(search, f.org.ID, agent.ID)
		require.NoError(t, f.app.SearchClinicStaff(search))
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(search))
	})

	t.Run("a user in another organisation can't be linked from here", func(t *testing.T) {
		other := testutil.CreateTestOrganization(t, f.app.DB)
		stranger := testutil.CreateTestUser(t, f.app.DB, other.ID)
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(link(t, stranger.ID, "sara@clinic.test")))
	})
}

func TestClinicStaffNeedsTheClinicSystem(t *testing.T) {
	f := newHeldFixture(t)
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	require.NoError(t, f.app.ListClinicLinks(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

func TestLinkingForgetsPhotoAnswers(t *testing.T) {
	f := newHeldFixture(t)
	fc, srv := newFakeClinic(t)
	f.clinic(srv.URL)
	if f.app.Redis == nil {
		t.Skip("needs Redis")
	}
	contact := f.contact(t)
	fc.reply = func(req clinicRequest) (int, string) {
		if req.Action == "staff_users" {
			return staffClinic(req)
		}
		if req.ActingUser != "noor@clinic.test" {
			return 403, `{}`
		}
		return 200, photoReply("ab12cd34ef")
	}
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(f.photo(t, f.user, contact.ID, "")))

	req := testutil.NewJSONRequest(t, map[string]any{"clinic_user": "noor@clinic.test"})
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	testutil.SetPathParam(req, "id", f.user.ID.String())
	require.NoError(t, f.app.SetClinicUser(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(f.photo(t, f.user, contact.ID, "")), "asked again as the new staff login, not the remembered refusal")
}
