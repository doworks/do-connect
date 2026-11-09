package handlers

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

var fakeJPEG = []byte("\xff\xd8\xff\xe0 a small photo")

func photoReply(version string) string {
	return fmt.Sprintf(`{"message": {"patient": "PAT-1", "content_type": "image/jpeg", "data": %q, "version": %q}}`,
		base64.StdEncoding.EncodeToString(fakeJPEG), version)
}

func (f heldFixture) photo(t *testing.T, user *models.User, contactID uuid.UUID, etag string, extra ...func(*fastglue.Request)) *fastglue.Request {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, f.org.ID, user.ID)
	testutil.SetPathParam(req, "id", contactID.String())
	if etag != "" {
		req.RequestCtx.Request.Header.Set("If-None-Match", etag)
	}
	for _, e := range extra {
		e(req)
	}
	require.NoError(t, f.app.ContactPhoto(req))
	return req
}

func (fc *fakeClinic) count(action string) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	n := 0
	for _, r := range fc.requests {
		if r.Action == action {
			n++
		}
	}
	return n
}

func TestContactPhoto(t *testing.T) {
	t.Run("no clinic system, no photo address", func(t *testing.T) {
		f := newHeldFixture(t)
		contact := f.contact(t)
		assert.Empty(t, f.app.buildContactResponse(contact, f.org.ID).AvatarURL)
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(f.photo(t, f.user, contact.ID, "")))
	})

	t.Run("the photo comes from the clinic as the viewer, then from the cache", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)
		version := "ab12" + uuid.NewString()[:8]
		fc.reply = func(clinicRequest) (int, string) { return 200, photoReply(version) }
		contact := f.contact(t)
		assert.Equal(t, "/api/contacts/"+contact.ID.String()+"/photo", f.app.buildContactResponse(contact, f.org.ID).AvatarURL)

		req := f.photo(t, f.user, contact.ID, "")
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req)))
		assert.Equal(t, fakeJPEG, testutil.GetResponseBody(req))
		assert.Equal(t, "image/jpeg", string(req.RequestCtx.Response.Header.ContentType()))
		assert.Contains(t, string(req.RequestCtx.Response.Header.Peek("Cache-Control")), "private")
		etag := string(req.RequestCtx.Response.Header.Peek("ETag"))
		assert.Equal(t, `"`+version+`"`, etag)

		fc.mu.Lock()
		first := fc.requests[0]
		valid := fc.valid[0]
		fc.mu.Unlock()
		assert.True(t, valid, "signed like every panel request")
		assert.Equal(t, "photo", first.Action)
		assert.Equal(t, f.user.Email, first.ActingUser)
		assert.Equal(t, contact.PhoneNumber, first.Args["phone"])

		assert.Equal(t, fakeJPEG, testutil.GetResponseBody(f.photo(t, f.user, contact.ID, "")))
		assert.Equal(t, fasthttp.StatusNotModified, testutil.GetResponseStatusCode(f.photo(t, f.user, contact.ID, etag)))
		assert.Equal(t, 1, fc.count("photo"), "asked once")

		colleague := testutil.CreateTestUser(t, f.app.DB, f.org.ID, testutil.WithRoleID(f.user.RoleID))
		assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(f.photo(t, colleague, contact.ID, "")))
		assert.Equal(t, 2, fc.count("photo"), "the clinic decides for each person")
		fc.mu.Lock()
		assert.Equal(t, colleague.Email, fc.requests[1].ActingUser)
		fc.mu.Unlock()
	})

	t.Run("a patient on the panel can be asked for by name", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)
		fc.reply = func(clinicRequest) (int, string) { return 200, photoReply("cd34" + uuid.NewString()[:8]) }
		contact := f.contact(t)
		f.photo(t, f.user, contact.ID, "", func(r *fastglue.Request) { testutil.SetQueryParam(r, "patient", "PAT-7") })
		fc.mu.Lock()
		defer fc.mu.Unlock()
		assert.Equal(t, "PAT-7", fc.requests[0].Args["patient"])
	})

	t.Run("no photo, or the clinic down, is a 404 that isn't asked again at once", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)
		for _, reply := range []struct {
			status int
			body   string
		}{
			{200, `{"message": {"patient": null}}`},
			{500, `{"exc_type": "Boom"}`},
			{200, photoReply(`x" bad`)},
		} {
			fc.reply = func(clinicRequest) (int, string) { return reply.status, reply.body }
			contact := f.contact(t)
			before := fc.count("photo")
			req := f.photo(t, f.user, contact.ID, "")
			assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req), reply.body)
			assert.Equal(t, "no-store", string(req.RequestCtx.Response.Header.Peek("Cache-Control")))
			f.photo(t, f.user, contact.ID, "")
			assert.Equal(t, before+1, fc.count("photo"), reply.body)
		}
	})

	t.Run("only chats the viewer may open, and not with an API key", func(t *testing.T) {
		f := newHeldFixture(t)
		fc, srv := newFakeClinic(t)
		f.clinic(srv.URL)
		fc.reply = func(clinicRequest) (int, string) { return 200, photoReply("ef56" + uuid.NewString()[:8]) }
		otherOrg := testutil.CreateTestOrganization(t, f.app.DB)
		elsewhere := testutil.CreateTestContact(t, f.app.DB, otherOrg.ID)
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(f.photo(t, f.user, elsewhere.ID, "")))

		contact := f.contact(t)
		viaKey := f.photo(t, f.user, contact.ID, "", func(r *fastglue.Request) { r.RequestCtx.SetUserValue(middleware.ContextKeyViaAPIKey, true) })
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(viaKey))
		assert.Equal(t, 0, fc.count("photo"))
	})
}

func TestMobileCallPhoto(t *testing.T) {
	f := newHeldFixture(t)
	fc, srv := newFakeClinic(t)
	f.clinic(srv.URL)
	fc.reply = func(clinicRequest) (int, string) { return 200, photoReply("0a1b" + uuid.NewString()[:8]) }
	contact := f.contact(t)
	callLog := models.CallLog{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  f.org.ID,
		WhatsAppAccount: f.account.Name,
		ContactID:       contact.ID,
		WhatsAppCallID:  "wacid." + uuid.NewString(),
		CallerPhone:     contact.PhoneNumber,
		Status:          models.CallStatusRinging,
	}
	require.NoError(t, f.app.DB.Create(&callLog).Error)
	transfer := models.CallTransfer{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: f.org.ID,
		CallLogID:      callLog.ID,
		ContactID:      contact.ID,
		CallerPhone:    contact.PhoneNumber,
		Status:         models.CallTransferStatusWaiting,
	}
	require.NoError(t, f.app.DB.Create(&transfer).Error)

	get := func(transferID, pass string) *fastglue.Request {
		req := testutil.NewGETRequest(t)
		testutil.SetQueryParam(req, "transfer_id", transferID)
		testutil.SetQueryParam(req, "pass", pass)
		require.NoError(t, f.app.MobileCallPhoto(req))
		return req
	}

	pass := f.app.declinePass(transfer.ID.String(), f.user.ID, time.Now().Add(time.Minute).Unix())
	req := get(transfer.ID.String(), pass)
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req)))
	assert.Equal(t, fakeJPEG, testutil.GetResponseBody(req))
	fc.mu.Lock()
	assert.Equal(t, f.user.Email, fc.requests[0].ActingUser, "as the person the phone rang for")
	assert.Equal(t, contact.PhoneNumber, fc.requests[0].Args["phone"])
	fc.mu.Unlock()

	expired := f.app.declinePass(transfer.ID.String(), f.user.ID, time.Now().Add(-time.Second).Unix())
	assert.Equal(t, fasthttp.StatusUnauthorized, testutil.GetResponseStatusCode(get(transfer.ID.String(), expired)))
	assert.Equal(t, fasthttp.StatusUnauthorized, testutil.GetResponseStatusCode(get(uuid.NewString(), pass)), "another call")
	assert.Equal(t, fasthttp.StatusUnauthorized, testutil.GetResponseStatusCode(get(transfer.ID.String(), "nonsense")))

	t.Run("the ring tells the phone where the photo is", func(t *testing.T) {
		p := newPushFixture(t)
		p.app.Config.Clinic = f.app.Config.Clinic
		taker := p.userWithRole(t, callTaker)
		p.phone(t, taker, "taker-phone")
		offer := p.offer(p.contact(t))
		p.app.RingPhones(p.org.ID, []uuid.UUID{taker.ID}, offer)
		p.fcm.mu.Lock()
		data := p.fcm.messages[0]["data"].(map[string]any)
		p.fcm.mu.Unlock()
		photo, _ := data["photo_path"].(string)
		require.True(t, strings.HasPrefix(photo, "/api/mobile/call-photo?"), photo)
		query, err := url.ParseQuery(strings.SplitN(photo, "?", 2)[1])
		require.NoError(t, err)
		assert.Equal(t, offer["id"], query.Get("transfer_id"))
		assert.Equal(t, data["decline_pass"], query.Get("pass"))
	})
}
