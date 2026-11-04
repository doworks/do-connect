package handlers_test

import (
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

const mobileTestPassword = "phone-password-123"

type mobileTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
	DeviceID     uuid.UUID `json:"device_id"`
	Device       struct {
		ID       uuid.UUID `json:"id"`
		Name     string    `json:"name"`
		Platform string    `json:"platform"`
		Current  bool      `json:"current"`
	} `json:"device"`
	User struct {
		ID       uuid.UUID `json:"id"`
		FullName string    `json:"full_name"`
	} `json:"user"`
}

func mobileUser(t *testing.T, app *handlers.App, orgID uuid.UUID, opts ...testutil.UserOption) *models.User {
	t.Helper()
	opts = append([]testutil.UserOption{
		testutil.WithEmail(testutil.UniqueEmail("phone")),
		testutil.WithPassword(mobileTestPassword),
	}, opts...)
	return testutil.CreateTestUser(t, app.DB, orgID, opts...)
}

func mobileLogin(t *testing.T, app *handlers.App, email, password string) *fastglue.Request {
	t.Helper()
	req := testutil.NewJSONRequest(t, map[string]any{
		"email":    email,
		"password": password,
		"device":   map[string]any{"name": "Test phone", "platform": "Android", "push_token": "push-1"},
	})
	require.NoError(t, app.MobileLogin(req))
	return req
}

func mustMobileLogin(t *testing.T, app *handlers.App, user *models.User) mobileTokens {
	t.Helper()
	req := mobileLogin(t, app, user.Email, mobileTestPassword)
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req)))
	var tokens mobileTokens
	testutil.ParseEnvelopeResponse(t, req, &tokens)
	return tokens
}

func mobileRefresh(t *testing.T, app *handlers.App, refreshToken string) (int, mobileTokens) {
	t.Helper()
	req := testutil.NewJSONRequest(t, map[string]string{"refresh_token": refreshToken})
	require.NoError(t, app.MobileRefresh(req))
	var tokens mobileTokens
	status := testutil.GetResponseStatusCode(req)
	if status == fasthttp.StatusOK {
		testutil.ParseEnvelopeResponse(t, req, &tokens)
	}
	return status, tokens
}

// authed runs a request through the real auth middleware with a phone's
// access token; it returns nil when the middleware refused it.
func authed(t *testing.T, app *handlers.App, req *fastglue.Request, accessToken string) *fastglue.Request {
	t.Helper()
	testutil.SetAuthHeader(req, accessToken)
	return middleware.AuthWithDB(testutil.TestJWTSecret, app.DB)(req)
}

func TestMobileLogin_ReturnsTokensAndDevice(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID, testutil.WithFullName("Phone User"))

	tokens := mustMobileLogin(t, app, user)

	assert.NotEmpty(t, tokens.AccessToken)
	assert.Contains(t, tokens.RefreshToken, "wmd_")
	assert.Equal(t, 15*60, tokens.ExpiresIn)
	assert.NotEqual(t, uuid.Nil, tokens.DeviceID)
	assert.Equal(t, tokens.DeviceID, tokens.Device.ID)
	assert.Equal(t, "Test phone", tokens.Device.Name)
	assert.Equal(t, "android", tokens.Device.Platform)
	assert.Equal(t, user.ID, tokens.User.ID)
	assert.Equal(t, "Phone User", tokens.User.FullName)

	var device models.MobileDevice
	require.NoError(t, app.DB.First(&device, "id = ?", tokens.DeviceID).Error)
	assert.Equal(t, user.ID, device.UserID)
	assert.Equal(t, org.ID, device.OrganizationID)
	assert.Equal(t, "push-1", device.PushToken)
	assert.Len(t, device.TokenHash, 64)
	assert.NotContains(t, device.TokenHash, tokens.RefreshToken, "only a hash is stored")

	// The access token works as a Bearer token and carries the device.
	req := authed(t, app, testutil.NewGETRequest(t), tokens.AccessToken)
	require.NotNil(t, req)
	deviceID, ok := middleware.GetDeviceID(req)
	assert.True(t, ok)
	assert.Equal(t, tokens.DeviceID, deviceID)
}

func TestMobileLogin_WrongPasswordAndUnknownEmailLookAlike(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)

	req := mobileLogin(t, app, user.Email, "not-the-password")
	testutil.AssertErrorResponse(t, req, fasthttp.StatusUnauthorized, "Invalid credentials")

	req = mobileLogin(t, app, testutil.UniqueEmail("nobody"), mobileTestPassword)
	testutil.AssertErrorResponse(t, req, fasthttp.StatusUnauthorized, "Invalid credentials")

	var count int64
	app.DB.Model(&models.MobileDevice{}).Where("user_id = ?", user.ID).Count(&count)
	assert.Zero(t, count)
}

func TestMobileLogin_DisabledUserOnlyToldWithRightPassword(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID, testutil.WithInactive())

	req := mobileLogin(t, app, user.Email, "not-the-password")
	testutil.AssertErrorResponse(t, req, fasthttp.StatusUnauthorized, "Invalid credentials")

	req = mobileLogin(t, app, user.Email, mobileTestPassword)
	testutil.AssertErrorResponse(t, req, fasthttp.StatusUnauthorized, "Account is disabled")
}

func TestMobileLogin_RateLimited(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)

	// Same wiring as the router: the login rate limit in front of the handler.
	limited := middleware.RateLimit(middleware.RateLimitOpts{
		Redis: app.Redis, Log: app.Log, Max: 2, Window: time.Minute,
		KeyPrefix: "mobile_login_test_" + uuid.New().String()[:8],
	})
	attempt := func() int {
		req := testutil.NewJSONRequest(t, map[string]string{"email": user.Email, "password": "wrong-password-1"})
		req.RequestCtx.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP("10.9.8.7"), Port: 4000})
		if limited(req) == nil {
			return testutil.GetResponseStatusCode(req)
		}
		require.NoError(t, app.MobileLogin(req))
		return testutil.GetResponseStatusCode(req)
	}

	assert.Equal(t, fasthttp.StatusUnauthorized, attempt())
	assert.Equal(t, fasthttp.StatusUnauthorized, attempt())
	assert.Equal(t, fasthttp.StatusTooManyRequests, attempt())
}

func TestMobileRefresh_Rotates(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	first := mustMobileLogin(t, app, user)

	status, second := mobileRefresh(t, app, first.RefreshToken)
	require.Equal(t, fasthttp.StatusOK, status)
	assert.NotEqual(t, first.RefreshToken, second.RefreshToken)
	assert.NotEmpty(t, second.AccessToken)
	assert.Equal(t, first.DeviceID, second.DeviceID)
	assert.Equal(t, user.ID, second.User.ID)

	// The phone keeps rotating with each new token.
	status, third := mobileRefresh(t, app, second.RefreshToken)
	require.Equal(t, fasthttp.StatusOK, status)
	assert.NotEqual(t, second.RefreshToken, third.RefreshToken)

	// Once the newer token has been used, the older one is finished.
	status, _ = mobileRefresh(t, app, first.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)
}

func TestMobileRefresh_PreviousTokenWorksOnceWithinGrace(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	login := mustMobileLogin(t, app, user)

	// The server rotates, but the answer never reaches the phone.
	status, lost := mobileRefresh(t, app, login.RefreshToken)
	require.Equal(t, fasthttp.StatusOK, status)

	// The phone retries with the token it still has: accepted once.
	status, retried := mobileRefresh(t, app, login.RefreshToken)
	require.Equal(t, fasthttp.StatusOK, status)

	// Not twice.
	status, _ = mobileRefresh(t, app, login.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)

	// The token that never arrived is void; the retried one works.
	status, _ = mobileRefresh(t, app, lost.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)
	status, _ = mobileRefresh(t, app, retried.RefreshToken)
	assert.Equal(t, fasthttp.StatusOK, status)
}

func TestMobileRefresh_PreviousTokenRefusedAfterGrace(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	login := mustMobileLogin(t, app, user)

	status, next := mobileRefresh(t, app, login.RefreshToken)
	require.Equal(t, fasthttp.StatusOK, status)

	require.NoError(t, app.DB.Model(&models.MobileDevice{}).Where("id = ?", login.DeviceID).
		Update("previous_valid_until", time.Now().Add(-time.Minute)).Error)

	status, _ = mobileRefresh(t, app, login.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)
	status, _ = mobileRefresh(t, app, next.RefreshToken)
	assert.Equal(t, fasthttp.StatusOK, status)
}

func TestMobileRefresh_UnknownTokenRefused(t *testing.T) {
	app := newTestApp(t)
	for _, token := range []string{"", "garbage", "wmd_not-a-real-token"} {
		status, _ := mobileRefresh(t, app, token)
		assert.Equal(t, fasthttp.StatusUnauthorized, status, token)
	}
}

func TestMobileRefresh_IdlePhoneSignedOut(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	login := mustMobileLogin(t, app, user)

	require.NoError(t, app.DB.Model(&models.MobileDevice{}).Where("id = ?", login.DeviceID).
		Update("last_seen_at", time.Now().Add(-91*24*time.Hour)).Error)

	status, _ := mobileRefresh(t, app, login.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)
}

func TestMobileDevice_RemovedPhoneSignedOut(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "phone-admin", []string{"users:read", "users:write"})
	admin := mobileUser(t, app, org.ID, testutil.WithRoleID(&adminRole.ID))
	staff := mobileUser(t, app, org.ID)
	adminPhone := mustMobileLogin(t, app, admin)
	staffPhone := mustMobileLogin(t, app, staff)

	// The manager removes the staff member's phone.
	req := authed(t, app, testutil.NewRequest(t), adminPhone.AccessToken)
	require.NotNil(t, req)
	testutil.SetPathParam(req, "id", staffPhone.DeviceID.String())
	require.NoError(t, app.DeleteMobileDevice(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req)))

	// Its access token stops working at once, and so does its refresh token.
	assert.Nil(t, authed(t, app, testutil.NewGETRequest(t), staffPhone.AccessToken))
	status, _ := mobileRefresh(t, app, staffPhone.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)

	var device models.MobileDevice
	require.NoError(t, app.DB.First(&device, "id = ?", staffPhone.DeviceID).Error)
	require.NotNil(t, device.RevokedAt)
	assert.Equal(t, admin.ID, *device.RevokedBy)
	assert.Empty(t, device.PushToken)

	// The manager's own phone is untouched.
	assert.NotNil(t, authed(t, app, testutil.NewGETRequest(t), adminPhone.AccessToken))
}

func TestMobileDevice_RemovingSomeoneElsesPhoneNeedsPermission(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	first := mobileUser(t, app, org.ID)
	second := mobileUser(t, app, org.ID)
	firstPhone := mustMobileLogin(t, app, first)
	secondPhone := mustMobileLogin(t, app, second)

	req := authed(t, app, testutil.NewRequest(t), firstPhone.AccessToken)
	testutil.SetPathParam(req, "id", secondPhone.DeviceID.String())
	require.NoError(t, app.DeleteMobileDevice(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))

	// Another organization's phone is not even found.
	otherOrg := testutil.CreateTestOrganization(t, app.DB)
	outsider := mobileUser(t, app, otherOrg.ID)
	outsiderPhone := mustMobileLogin(t, app, outsider)
	req = authed(t, app, testutil.NewRequest(t), firstPhone.AccessToken)
	testutil.SetPathParam(req, "id", outsiderPhone.DeviceID.String())
	require.NoError(t, app.DeleteMobileDevice(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))

	// Anyone may remove their own phone.
	req = authed(t, app, testutil.NewRequest(t), firstPhone.AccessToken)
	testutil.SetPathParam(req, "id", firstPhone.DeviceID.String())
	require.NoError(t, app.DeleteMobileDevice(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}

func TestMobileDevice_ListOwnOrAll(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "phone-viewer", []string{"users:read"})
	admin := mobileUser(t, app, org.ID, testutil.WithRoleID(&adminRole.ID))
	staff := mobileUser(t, app, org.ID)
	adminPhone := mustMobileLogin(t, app, admin)
	staffPhone := mustMobileLogin(t, app, staff)

	list := func(token string, scope string) []map[string]any {
		req := authed(t, app, testutil.NewGETRequest(t), token)
		require.NotNil(t, req)
		if scope != "" {
			testutil.SetQueryParam(req, "scope", scope)
		}
		require.NoError(t, app.ListMobileDevices(req))
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
		var resp struct {
			Devices []map[string]any `json:"devices"`
		}
		testutil.ParseEnvelopeResponse(t, req, &resp)
		return resp.Devices
	}

	own := list(staffPhone.AccessToken, "")
	require.Len(t, own, 1)
	assert.Equal(t, staffPhone.DeviceID.String(), own[0]["id"])
	assert.Equal(t, true, own[0]["current"])
	assert.Equal(t, true, own[0]["push_enabled"])
	assert.NotContains(t, own[0], "push_token")

	assert.Len(t, list(adminPhone.AccessToken, ""), 2)
	assert.Len(t, list(adminPhone.AccessToken, "mine"), 1)

	// An admin can narrow the list to one user.
	req := authed(t, app, testutil.NewGETRequest(t), adminPhone.AccessToken)
	testutil.SetQueryParam(req, "user_id", staff.ID.String())
	require.NoError(t, app.ListMobileDevices(req))
	var resp struct {
		Devices []map[string]any `json:"devices"`
	}
	testutil.ParseEnvelopeResponse(t, req, &resp)
	require.Len(t, resp.Devices, 1)
	assert.Equal(t, staffPhone.DeviceID.String(), resp.Devices[0]["id"])
	assert.Equal(t, false, resp.Devices[0]["current"])
}

func TestMobileRefresh_DisabledUserSignedOut(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	login := mustMobileLogin(t, app, user)

	require.NoError(t, app.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("is_active", false).Error)

	assert.Nil(t, authed(t, app, testutil.NewGETRequest(t), login.AccessToken))
	status, _ := mobileRefresh(t, app, login.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)

	// Turning the account back on does not bring the phone back.
	require.NoError(t, app.DB.Model(&models.User{}).Where("id = ?", user.ID).Update("is_active", true).Error)
	status, _ = mobileRefresh(t, app, login.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)
}

func TestMobileDevice_DisablingUserSignsOutPhones(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "user-admin", []string{"users:read", "users:write"})
	admin := mobileUser(t, app, org.ID, testutil.WithRoleID(&adminRole.ID))
	staff := mobileUser(t, app, org.ID)
	staffPhone := mustMobileLogin(t, app, staff)

	req := testutil.NewJSONRequest(t, map[string]any{"is_active": false})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", staff.ID.String())
	require.NoError(t, app.UpdateUser(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req)))

	var device models.MobileDevice
	require.NoError(t, app.DB.First(&device, "id = ?", staffPhone.DeviceID).Error)
	assert.NotNil(t, device.RevokedAt)
}

func TestMobileLogout_DeletesDevice(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	login := mustMobileLogin(t, app, user)

	req := testutil.NewJSONRequest(t, map[string]string{"refresh_token": login.RefreshToken})
	require.NoError(t, app.MobileLogout(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var count int64
	app.DB.Model(&models.MobileDevice{}).Where("id = ?", login.DeviceID).Count(&count)
	assert.Zero(t, count)
	assert.Nil(t, authed(t, app, testutil.NewGETRequest(t), login.AccessToken))
	status, _ := mobileRefresh(t, app, login.RefreshToken)
	assert.Equal(t, fasthttp.StatusUnauthorized, status)

	// Signing out again, or with nothing, still succeeds.
	req = testutil.NewJSONRequest(t, map[string]string{"refresh_token": login.RefreshToken})
	require.NoError(t, app.MobileLogout(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}

func TestMobileAccessToken_CannotBecomeBrowserSession(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	login := mustMobileLogin(t, app, user)

	req := testutil.NewJSONRequest(t, map[string]string{"refresh_token": login.AccessToken})
	require.NoError(t, app.RefreshToken(req))
	assert.Equal(t, fasthttp.StatusUnauthorized, testutil.GetResponseStatusCode(req))

	req = authed(t, app, testutil.NewJSONRequest(t, map[string]string{"organization_id": org.ID.String()}), login.AccessToken)
	require.NotNil(t, req)
	require.NoError(t, app.SwitchOrg(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

func TestMobilePushToken_Updated(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := mobileUser(t, app, org.ID)
	login := mustMobileLogin(t, app, user)

	req := authed(t, app, testutil.NewJSONRequest(t, map[string]string{"push_token": "push-2"}), login.AccessToken)
	require.NoError(t, app.UpdateMobilePushToken(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var device models.MobileDevice
	require.NoError(t, app.DB.First(&device, "id = ?", login.DeviceID).Error)
	assert.Equal(t, "push-2", device.PushToken)

	// A browser session has no device to update.
	req = testutil.NewJSONRequest(t, map[string]string{"push_token": "push-3"})
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.UpdateMobilePushToken(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}
