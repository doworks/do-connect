package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (f heldFixture) wsToken(t *testing.T, deviceID *uuid.UUID) string {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	if deviceID != nil {
		req.RequestCtx.SetUserValue(middleware.ContextKeyDeviceID, *deviceID)
	}
	require.NoError(t, f.app.GetWSToken(req))
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &out))
	require.NotEmpty(t, out.Data.Token)
	return out.Data.Token
}

func TestWebSocketSignIn(t *testing.T) {
	newFixture := func(t *testing.T) heldFixture {
		f := newHeldFixture(t)
		f.app.Config.JWT = config.JWTConfig{Secret: testutil.TestJWTSecret, AccessExpiryMins: 15, RefreshExpiryDays: 7}
		return f
	}

	t.Run("the socket token works, other tokens don't", func(t *testing.T) {
		f := newFixture(t)
		validate := f.app.validateWSTokenFn()

		userID, orgID, err := validate(f.wsToken(t, nil))
		require.NoError(t, err)
		assert.Equal(t, f.user.ID, userID)
		assert.Equal(t, f.org.ID, orgID)

		access, err := f.app.generateAccessToken(f.user)
		require.NoError(t, err)
		_, _, err = validate(access)
		assert.Error(t, err, "an access token is not a socket token")

		refresh, err := f.app.generateRefreshToken(f.user)
		require.NoError(t, err)
		_, _, err = validate(refresh)
		assert.Error(t, err, "a refresh token is not a socket token")
	})

	t.Run("a phone's socket token stops working once the phone is removed", func(t *testing.T) {
		f := newFixture(t)
		device := &models.MobileDevice{
			ID:             uuid.New(),
			OrganizationID: f.org.ID,
			UserID:         f.user.ID,
			Name:           "Test phone",
			Platform:       "android",
			TokenHash:      uuid.NewString(),
			LastSeenAt:     time.Now(),
		}
		require.NoError(t, f.app.DB.Create(device).Error)
		validate := f.app.validateWSTokenFn()

		token := f.wsToken(t, &device.ID)
		_, _, err := validate(token)
		require.NoError(t, err)

		require.NoError(t, f.app.DB.Model(device).Update("revoked_at", time.Now()).Error)
		_, _, err = validate(token)
		assert.Error(t, err)
	})
}
