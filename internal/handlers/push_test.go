package handlers

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/push"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerodha/logf"
)

// fakeFCM records what the server sends to Firebase.
type fakeFCM struct {
	mu       sync.Mutex
	messages []map[string]any
	dead     map[string]bool
}

func (f *fakeFCM) tokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.messages {
		out = append(out, m["token"].(string))
	}
	sort.Strings(out)
	return out
}

func newFakeFCM(t *testing.T) (*fakeFCM, *push.FCM) {
	t.Helper()
	fake := &fakeFCM{dead: map[string]bool{}}
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-access", "expires_in": 3600})
	}))
	sendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message map[string]any `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		token, _ := body.Message["token"].(string)
		fake.mu.Lock()
		fake.messages = append(fake.messages, body.Message)
		dead := fake.dead[token]
		fake.mu.Unlock()
		if dead {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"projects/test-project/messages/1"}`))
	}))
	t.Cleanup(tokenSrv.Close)
	t.Cleanup(sendSrv.Close)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	creds, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "test-project",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "sender@test-project.iam.gserviceaccount.com",
	})
	require.NoError(t, err)
	client, err := push.NewFCM(creds, push.Options{TokenURL: tokenSrv.URL, SendURL: sendSrv.URL})
	require.NoError(t, err)
	return fake, client
}

type pushFixture struct {
	heldFixture
	fcm *fakeFCM
	log *bytes.Buffer
}

func newPushFixture(t *testing.T) pushFixture {
	t.Helper()
	f := newHeldFixture(t)
	fake, client := newFakeFCM(t)
	f.app.Push = client
	logs := &bytes.Buffer{}
	f.app.Log = logf.New(logf.Opts{Writer: &syncWriter{w: logs}, Level: logf.DebugLevel})
	return pushFixture{heldFixture: f, fcm: fake, log: logs}
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (f pushFixture) userWithRole(t *testing.T, perms []string, opts ...testutil.UserOption) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, f.app.DB, f.org.ID, "role-"+uuid.NewString()[:8], perms)
	return testutil.CreateTestUser(t, f.app.DB, f.org.ID, append([]testutil.UserOption{testutil.WithRoleID(&role.ID)}, opts...)...)
}

func (f pushFixture) phone(t *testing.T, user *models.User, token string, opts ...func(*models.MobileDevice)) *models.MobileDevice {
	t.Helper()
	device := &models.MobileDevice{
		OrganizationID: f.org.ID,
		UserID:         user.ID,
		Name:           "Phone",
		Platform:       "android",
		PushToken:      token,
		TokenHash:      uuid.NewString(),
		LastSeenAt:     time.Now(),
	}
	for _, opt := range opts {
		opt(device)
	}
	require.NoError(t, f.app.DB.Create(device).Error)
	return device
}

func (f pushFixture) arrives(t *testing.T, contact *models.Contact, msgType, text string) {
	t.Helper()
	own := *contact
	f.app.saveIncomingMessageAt(f.account, &own, "wamid."+uuid.NewString(), msgType, text, nil, "", time.Now())
	f.app.WaitForBackgroundTasks()
}

var chatAgent = []string{"chat:read", "chat:write", "contacts:read"}

func TestPushIncomingMessage(t *testing.T) {
	t.Run("without push credentials nothing is sent", func(t *testing.T) {
		f := newPushFixture(t)
		f.app.Push = nil
		f.phone(t, f.user, "token-admin")
		f.arrives(t, f.contact(t), "text", "Hello")
		assert.Empty(t, f.fcm.tokens())
	})

	t.Run("an assigned chat notifies only its owner's phones", func(t *testing.T) {
		f := newPushFixture(t)
		owner := f.userWithRole(t, chatAgent)
		other := f.userWithRole(t, chatAgent)
		f.phone(t, owner, "token-owner-1")
		f.phone(t, owner, "token-owner-2")
		f.phone(t, other, "token-other")
		f.phone(t, f.user, "token-admin")
		contact := f.contact(t, func(c *models.Contact) { c.AssignedUserID = &owner.ID })

		f.arrives(t, contact, "text", "Hello")
		assert.Equal(t, []string{"token-owner-1", "token-owner-2"}, f.fcm.tokens())
	})

	t.Run("an unassigned chat notifies everyone who can see it", func(t *testing.T) {
		f := newPushFixture(t)
		agent := f.userWithRole(t, chatAgent)
		ownOnly := f.userWithRole(t, []string{"chat:read", "chat:write"}) // sees only chats assigned to them
		noChat := f.userWithRole(t, []string{"contacts:read"})
		disabled := f.userWithRole(t, chatAgent, testutil.WithInactive())
		f.phone(t, f.user, "token-admin")
		f.phone(t, agent, "token-agent")
		f.phone(t, ownOnly, "token-own-only")
		f.phone(t, noChat, "token-no-chat")
		f.phone(t, disabled, "token-disabled")
		f.phone(t, agent, "") // signed in, notifications never set up

		elsewhere := testutil.CreateTestOrganization(t, f.app.DB)
		outsider := testutil.CreateTestUser(t, f.app.DB, elsewhere.ID, testutil.WithRoleID(&testutil.CreateAdminRole(t, f.app.DB, elsewhere.ID).ID))
		require.NoError(t, f.app.DB.Create(&models.MobileDevice{
			OrganizationID: elsewhere.ID, UserID: outsider.ID, PushToken: "token-elsewhere", TokenHash: uuid.NewString(), LastSeenAt: time.Now(),
		}).Error)

		f.arrives(t, f.contact(t), "text", "Hello")
		assert.Equal(t, []string{"token-admin", "token-agent"}, f.fcm.tokens())
	})

	t.Run("signed-out phones are skipped", func(t *testing.T) {
		f := newPushFixture(t)
		owner := f.userWithRole(t, chatAgent)
		revoked := time.Now().Add(-time.Hour)
		f.phone(t, owner, "token-current")
		f.phone(t, owner, "token-removed", func(d *models.MobileDevice) { d.RevokedAt = &revoked })
		contact := f.contact(t, func(c *models.Contact) { c.AssignedUserID = &owner.ID })

		f.arrives(t, contact, "text", "Hello")
		assert.Equal(t, []string{"token-current"}, f.fcm.tokens())
	})

	t.Run("a token FCM no longer knows is cleared", func(t *testing.T) {
		f := newPushFixture(t)
		owner := f.userWithRole(t, chatAgent)
		gone := f.phone(t, owner, "token-uninstalled")
		kept := f.phone(t, owner, "token-fine")
		f.fcm.dead["token-uninstalled"] = true
		contact := f.contact(t, func(c *models.Contact) { c.AssignedUserID = &owner.ID })

		f.arrives(t, contact, "text", "Hello")
		var reloaded models.MobileDevice
		require.NoError(t, f.app.DB.First(&reloaded, "id = ?", gone.ID).Error)
		assert.Empty(t, reloaded.PushToken)
		assert.Nil(t, reloaded.RevokedAt, "the phone stays signed in")
		var other models.MobileDevice
		require.NoError(t, f.app.DB.First(&other, "id = ?", kept.ID).Error)
		assert.Equal(t, "token-fine", other.PushToken)

		// The next message doesn't try the dead token again.
		f.arrives(t, contact, "text", "Still there?")
		assert.Equal(t, []string{"token-fine", "token-fine", "token-uninstalled"}, f.fcm.tokens())
	})

	t.Run("the payload names the chat and leaves the text out of the logs", func(t *testing.T) {
		f := newPushFixture(t)
		f.phone(t, f.user, "token-admin")
		contact := f.contact(t, func(c *models.Contact) { c.ProfileName = "Mariam Saleh" })
		secret := "My results came back, is the lump serious?"

		f.arrives(t, contact, "text", secret)
		require.Len(t, f.fcm.messages, 1)
		msg := f.fcm.messages[0]
		assert.Equal(t, map[string]any{"title": "Mariam Saleh", "body": secret}, msg["notification"])
		android := msg["android"].(map[string]any)
		assert.Equal(t, "HIGH", android["priority"])
		assert.Equal(t, map[string]any{"channel_id": "messages", "tag": contact.ID.String(), "visibility": "PRIVATE"}, android["notification"])
		data := msg["data"].(map[string]any)
		assert.Equal(t, contact.ID.String(), data["contact_id"])
		assert.NotEmpty(t, data["message_id"])

		logs := f.log.String()
		assert.Contains(t, logs, "Sent new-message push")
		assert.NotContains(t, logs, secret)
		assert.NotContains(t, logs, "lump")
		assert.NotContains(t, logs, "Mariam")
	})

	t.Run("attachments say what came in, numbers stand in for names", func(t *testing.T) {
		f := newPushFixture(t)
		f.phone(t, f.user, "token-admin")
		contact := f.contact(t, func(c *models.Contact) { c.ProfileName = ""; c.PhoneNumber = "15550001111" })

		f.arrives(t, contact, "audio", "")
		f.arrives(t, contact, "image", "a caption")
		f.arrives(t, contact, "document", "")
		require.Len(t, f.fcm.messages, 3)
		var bodies []string
		for _, m := range f.fcm.messages {
			n := m["notification"].(map[string]any)
			assert.Equal(t, "+15550001111", n["title"])
			bodies = append(bodies, n["body"].(string))
		}
		assert.Equal(t, []string{"Voice message", "Photo", "Document"}, bodies)
	})

	t.Run("a message the chatbot handles notifies nobody", func(t *testing.T) {
		f := newPushFixture(t)
		f.phone(t, f.user, "token-admin")
		require.NoError(t, f.app.DB.Create(&models.ChatbotSettings{
			BaseModel:       models.BaseModel{ID: uuid.New()},
			OrganizationID:  f.org.ID,
			WhatsAppAccount: f.account.Name,
			IsEnabled:       true,
		}).Error)
		f.arrives(t, f.contact(t), "text", "Hi")
		assert.Empty(t, f.fcm.tokens())
	})
}

func TestPushPreview(t *testing.T) {
	long := ""
	for range 30 {
		long += "سلام "
	}
	preview := pushPreview("text", long)
	assert.Equal(t, pushPreviewMax, len([]rune(preview)), "cut by letters, not bytes")
	assert.Equal(t, "Hello", pushPreview("button_reply", "Hello"))
	assert.Equal(t, "Video", pushPreview("video", "caption"))
	assert.Equal(t, "Location", pushPreview("location", `{"latitude":1}`))
	assert.Equal(t, "New message", pushPreview("unsupported", "x"))
}
