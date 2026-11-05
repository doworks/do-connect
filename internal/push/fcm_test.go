package push_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/shridarpatil/whatomate/internal/push"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGoogle stands in for the OAuth token endpoint and FCM.
type fakeGoogle struct {
	key        *rsa.PrivateKey
	tokens     atomic.Int32
	sends      atomic.Int32
	lastBody   map[string]any
	lastAuth   string
	lastPath   string
	answer     func(w http.ResponseWriter)
	tokenSrv   *httptest.Server
	sendSrv    *httptest.Server
	assertions []jwt.MapClaims
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	g := &fakeGoogle{key: key}
	g.tokenSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", r.Form.Get("grant_type"))
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(r.Form.Get("assertion"), claims, func(tok *jwt.Token) (any, error) {
			assert.Equal(t, "RS256", tok.Method.Alg())
			return &key.PublicKey, nil
		})
		require.NoError(t, err, "the assertion is signed with the service account key")
		g.assertions = append(g.assertions, claims)
		g.tokens.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-" + string(rune('0'+g.tokens.Load())), "expires_in": 3600, "token_type": "Bearer"})
	}))
	g.sendSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.sends.Add(1)
		g.lastAuth = r.Header.Get("Authorization")
		g.lastPath = r.URL.Path
		g.lastBody = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&g.lastBody)
		if g.answer != nil {
			g.answer(w)
			return
		}
		_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/1"}`))
	}))
	t.Cleanup(g.tokenSrv.Close)
	t.Cleanup(g.sendSrv.Close)
	return g
}

func (g *fakeGoogle) credentials(t *testing.T) []byte {
	t.Helper()
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: must(x509.MarshalPKCS8PrivateKey(g.key))})
	data, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "test-project",
		"private_key_id": "kid-1",
		"private_key":    string(pemKey),
		"client_email":   "sender@test-project.iam.gserviceaccount.com",
		"token_uri":      "https://oauth2.invalid/token",
	})
	require.NoError(t, err)
	return data
}

func (g *fakeGoogle) client(t *testing.T, now func() time.Time) *push.FCM {
	t.Helper()
	f, err := push.NewFCM(g.credentials(t), push.Options{TokenURL: g.tokenSrv.URL, SendURL: g.sendSrv.URL, Now: now})
	require.NoError(t, err)
	return f
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestFCMSendsTheMessage(t *testing.T) {
	g := newFakeGoogle(t)
	f := g.client(t, nil)
	assert.Equal(t, "test-project", f.ProjectID())

	err := f.Send(context.Background(), push.Message{
		Token: "device-token", Title: "Fatima", Body: "Photo", Tag: "contact-1", ChannelID: "messages",
		Data: map[string]string{"contact_id": "contact-1", "message_id": "message-1"},
	})
	require.NoError(t, err)

	assert.Equal(t, "/v1/projects/test-project/messages:send", g.lastPath)
	assert.Equal(t, "Bearer access-1", g.lastAuth)
	assert.Equal(t, false, g.lastBody["validate_only"])
	msg := g.lastBody["message"].(map[string]any)
	assert.Equal(t, "device-token", msg["token"])
	assert.Equal(t, map[string]any{"title": "Fatima", "body": "Photo"}, msg["notification"])
	assert.Equal(t, map[string]any{"contact_id": "contact-1", "message_id": "message-1"}, msg["data"])
	android := msg["android"].(map[string]any)
	assert.Equal(t, "HIGH", android["priority"])
	assert.Equal(t, map[string]any{"channel_id": "messages", "tag": "contact-1", "visibility": "PRIVATE"}, android["notification"])

	claims := g.assertions[0]
	assert.Equal(t, "sender@test-project.iam.gserviceaccount.com", claims["iss"])
	assert.Equal(t, "https://www.googleapis.com/auth/firebase.messaging", claims["scope"])
	assert.Equal(t, g.tokenSrv.URL, claims["aud"])
}

func TestFCMValidateOnly(t *testing.T) {
	g := newFakeGoogle(t)
	require.NoError(t, g.client(t, nil).Validate(context.Background(), push.Message{Token: "t"}))
	assert.Equal(t, true, g.lastBody["validate_only"])
}

func TestFCMCachesTheAccessToken(t *testing.T) {
	g := newFakeGoogle(t)
	now := time.Now()
	f := g.client(t, func() time.Time { return now })

	for range 3 {
		require.NoError(t, f.Send(context.Background(), push.Message{Token: "t"}))
	}
	assert.Equal(t, int32(1), g.tokens.Load(), "one access token for many sends")

	now = now.Add(54 * time.Minute)
	require.NoError(t, f.Send(context.Background(), push.Message{Token: "t"}))
	assert.Equal(t, int32(1), g.tokens.Load(), "still good")

	now = now.Add(2 * time.Minute) // within five minutes of expiry
	require.NoError(t, f.Send(context.Background(), push.Message{Token: "t"}))
	assert.Equal(t, int32(2), g.tokens.Load(), "renewed shortly before it expires")
	assert.Equal(t, "Bearer access-2", g.lastAuth)
}

func TestFCMErrors(t *testing.T) {
	cases := map[string]struct {
		status  int
		body    string
		invalid bool
	}{
		"unregistered":                {404, `{"error":{"code":404,"status":"NOT_FOUND","message":"Requested entity was not found.","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, true},
		"bad token":                   {400, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"The registration token is not a valid FCM registration token","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`, true},
		"bad payload keeps the token": {400, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"Invalid value at 'message.android.notification.visibility'","details":[{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.android.notification.visibility"}]}]}}`, false},
		"quota":                       {429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, false},
		"server error":                {503, `oops`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := newFakeGoogle(t)
			g.answer = func(w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}
			err := g.client(t, nil).Send(context.Background(), push.Message{Token: "t"})
			require.Error(t, err)
			assert.Equal(t, tc.invalid, errors.Is(err, push.ErrInvalidToken), err.Error())
		})
	}
}

func TestFCMRejectsBadCredentials(t *testing.T) {
	for name, data := range map[string]string{
		"not json":        "nope",
		"no key":          `{"type":"service_account","project_id":"p","client_email":"e"}`,
		"not a key":       `{"type":"service_account","project_id":"p","client_email":"e","private_key":"x"}`,
		"user credential": `{"type":"authorized_user","project_id":"p","client_email":"e","private_key":"x"}`,
	} {
		_, err := push.NewFCM([]byte(data), push.Options{})
		assert.Error(t, err, name)
	}
	_, err := push.LoadFCM("/nonexistent/credentials.json", push.Options{})
	assert.Error(t, err)
}
