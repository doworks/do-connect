package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// sentToMeta records what would have gone to WhatsApp, in order.
type sentToMeta struct {
	mu      sync.Mutex
	sent    []map[string]any
	uploads []string
	refusal string
}

// fail makes Meta refuse every send from now on.
func (s *sentToMeta) fail(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refusal = reason
}

func (s *sentToMeta) RoundTrip(req *http.Request) (*http.Response, error) {
	var parsed map[string]any
	var raw []byte
	if req.Body != nil {
		raw, _ = io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &parsed)
	}
	if strings.HasSuffix(req.URL.Path, "/media") {
		s.mu.Lock()
		s.uploads = append(s.uploads, string(raw))
		s.mu.Unlock()
		recorder := httptest.NewRecorder()
		// Like Meta: a file part without a name counts as no file.
		if strings.Contains(string(raw), `filename=""`) {
			recorder.WriteHeader(http.StatusBadRequest)
			_, _ = recorder.WriteString(`{"error":{"message":"(#100) The parameter file is required.","code":100}}`)
			return recorder.Result(), nil
		}
		_, _ = recorder.WriteString(`{"id":"media.` + uuid.NewString() + `"}`)
		return recorder.Result(), nil
	}
	s.mu.Lock()
	refusal := s.refusal
	if refusal == "" {
		s.sent = append(s.sent, parsed)
	}
	s.mu.Unlock()
	recorder := httptest.NewRecorder()
	if refusal != "" {
		recorder.WriteHeader(http.StatusBadRequest)
		_, _ = recorder.WriteString(`{"error":{"code":132001,"message":"` + refusal + `"}}`)
		return recorder.Result(), nil
	}
	_, _ = recorder.WriteString(`{"messages":[{"id":"wamid.` + uuid.NewString() + `"}]}`)
	return recorder.Result(), nil
}

func (s *sentToMeta) ofType(kind string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, message := range s.sent {
		if message["type"] == kind {
			out = append(out, message)
		}
	}
	return out
}

type heldFixture struct {
	app     *App
	org     *models.Organization
	account *models.WhatsAppAccount
	user    *models.User
	meta    *sentToMeta
}

func newHeldFixture(t *testing.T) heldFixture {
	t.Helper()
	app := newProcessorTestApp(t)
	app.Config = &config.Config{}
	app.webhookQuickRetryDelay = time.Millisecond
	meta := &sentToMeta{}
	app.WhatsApp.HTTPClient = &http.Client{Transport: meta}
	t.Cleanup(app.WaitForBackgroundTasks)
	org, account := createProcessorTestOrg(t, app)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID), testutil.WithFullName("Noor Ali"))
	return heldFixture{app: app, org: org, account: account, user: user, meta: meta}
}

func (f heldFixture) template(t *testing.T, name, language, button string) {
	t.Helper()
	require.NoError(t, f.app.DB.Create(&models.Template{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  f.org.ID,
		WhatsAppAccount: f.account.Name,
		Name:            name,
		Language:        language,
		Category:        "UTILITY",
		Status:          "APPROVED",
		BodyContent:     "You have a message.",
		Buttons:         models.JSONBArray{map[string]any{"type": "QUICK_REPLY", "text": button}},
	}).Error)
}

func (f heldFixture) configure(t *testing.T) {
	t.Helper()
	f.template(t, "unlock_en", "en", "View message")
	f.template(t, "unlock_ar", "ar", "عرض الرسالة")
	require.NoError(t, f.app.DB.Model(f.org).Update("settings", models.JSONB{
		"unlock_template_en": "unlock_en",
		"unlock_template_ar": "unlock_ar",
	}).Error)
}

func (f heldFixture) contact(t *testing.T, opts ...func(*models.Contact)) *models.Contact {
	t.Helper()
	contact := testutil.CreateTestContactWith(t, f.app.DB, f.org.ID, testutil.WithContactAccount(f.account.Name))
	for _, opt := range opts {
		opt(contact)
	}
	require.NoError(t, f.app.DB.Save(contact).Error)
	return contact
}

func (f heldFixture) send(t *testing.T, contact *models.Contact, body string) MessageResponse {
	t.Helper()
	req := testutil.NewJSONRequest(t, map[string]any{"type": "text", "content": map[string]string{"body": body}})
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())
	require.NoError(t, f.app.SendMessage(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req), string(testutil.GetResponseBody(req)))
	var resp struct {
		Data MessageResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	f.app.WaitForBackgroundTasks()
	return resp.Data
}

func (f heldFixture) patientWrites(t *testing.T, contact *models.Contact, kind, text string) {
	t.Helper()
	f.app.saveIncomingMessage(f.account, contact, "wamid."+uuid.NewString(), kind, text, nil, "")
	f.app.WaitForBackgroundTasks()
}

func (f heldFixture) status(t *testing.T, id uuid.UUID) models.Message {
	t.Helper()
	var message models.Message
	require.NoError(t, f.app.DB.Unscoped().First(&message, id).Error)
	return message
}

func recentlyWrote(contact *models.Contact) {
	now := time.Now().Add(-time.Hour)
	contact.LastInboundAt = &now
}

func TestHoldingOutsideTheWindow(t *testing.T) {
	t.Run("the message waits and one notice goes out", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)

		first := f.send(t, contact, "Your results are normal")
		second := f.send(t, contact, "Please book a follow-up")

		assert.Equal(t, models.MessageStatusHeld, first.Status)
		assert.Equal(t, models.MessageStatusHeld, second.Status)
		assert.Equal(t, "Noor Ali", first.SentByName)
		assert.Empty(t, f.meta.ofType("text"), "nothing typed reaches the patient yet")
		assert.Len(t, f.meta.ofType("template"), 1, "the second message doesn't send a second notice")
	})

	t.Run("inside the window it sends normally", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		sent := f.send(t, f.contact(t, recentlyWrote), "Hello")
		assert.NotEqual(t, models.MessageStatusHeld, sent.Status)
		assert.Len(t, f.meta.ofType("text"), 1)
		assert.Empty(t, f.meta.ofType("template"))
	})

	t.Run("without a template set up nothing changes", func(t *testing.T) {
		f := newHeldFixture(t)
		sent := f.send(t, f.contact(t), "Hello")
		assert.NotEqual(t, models.MessageStatusHeld, sent.Status)
		assert.Len(t, f.meta.ofType("text"), 1)
	})

	t.Run("Arabic patients get the Arabic notice", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		f.send(t, f.contact(t, func(c *models.Contact) { c.ProfileName = "AR · Fatima" }), "مرحبا")
		templates := f.meta.ofType("template")
		require.Len(t, templates, 1)
		assert.Equal(t, "unlock_ar", templates[0]["template"].(map[string]any)["name"])
	})

	t.Run("with no Arabic template the whole notice is English", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "unlock_en", "en", "View message")
		require.NoError(t, f.app.DB.Model(f.org).Update("settings", models.JSONB{"unlock_template_en": "unlock_en", "unlock_template_ar": "unlock_ar"}).Error)
		f.send(t, f.contact(t, func(c *models.Contact) { c.Tags = models.JSONBArray{"Arabic"} }), "مرحبا")
		templates := f.meta.ofType("template")
		require.Len(t, templates, 1)
		assert.Equal(t, "unlock_en", templates[0]["template"].(map[string]any)["name"])
	})

	t.Run("a failed notice can be sent again", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		f.send(t, contact, "First")
		require.NoError(t, f.app.DB.Model(&models.Message{}).Where("template_name = ?", "unlock_en").Update("status", models.MessageStatusFailed).Error)
		f.send(t, contact, "Second")
		assert.Len(t, f.meta.ofType("template"), 2)
	})
}

func TestReleasingHeldMessages(t *testing.T) {
	t.Run("tapping the button sends them in order, without leaving the chat waiting", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		first := f.send(t, contact, "First")
		second := f.send(t, contact, "Second")

		f.patientWrites(t, contact, "button_reply", "View message")

		texts := f.meta.ofType("text")
		require.Len(t, texts, 2)
		assert.Equal(t, "First", texts[0]["text"].(map[string]any)["body"])
		assert.Equal(t, "Second", texts[1]["text"].(map[string]any)["body"])
		assert.Equal(t, models.MessageStatusSent, f.status(t, first.ID).Status)
		assert.Equal(t, models.MessageStatusSent, f.status(t, second.ID).Status)
		assert.NotEmpty(t, f.status(t, first.ID).WhatsAppMessageID)

		var saved models.Contact
		require.NoError(t, f.app.DB.First(&saved, contact.ID).Error)
		assert.Nil(t, saved.WaitingSince)
		assert.Equal(t, "Noor Ali", saved.LastReplyBy)

		var count int64
		f.app.DB.Model(&models.Message{}).Where("contact_id = ? AND direction = 'incoming' AND status <> 'read'", contact.ID).Count(&count)
		assert.Zero(t, count, "the tap isn't left unread")
	})

	t.Run("anything they write releases it, and their question stays waiting", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		held := f.send(t, contact, "Held")

		f.patientWrites(t, contact, "text", "Can I come on Thursday?")

		assert.Equal(t, models.MessageStatusSent, f.status(t, held.ID).Status)
		var saved models.Contact
		require.NoError(t, f.app.DB.First(&saved, contact.ID).Error)
		assert.NotNil(t, saved.WaitingSince, "staff still need to answer them")

		var count int64
		f.app.DB.Model(&models.Message{}).Where("contact_id = ? AND direction = 'incoming' AND status <> 'read'", contact.ID).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("a Yes on a reminder is still a real answer", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		f.send(t, contact, "Held")

		f.patientWrites(t, contact, "button_reply", "Yes")

		var saved models.Contact
		require.NoError(t, f.app.DB.First(&saved, contact.ID).Error)
		assert.NotNil(t, saved.WaitingSince)
	})

	t.Run("two messages at once send it only once", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		f.send(t, contact, "Held")

		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			// Each incoming message loads its own contact, as the real path does.
			own := *contact
			go func() {
				defer wg.Done()
				f.app.saveIncomingMessage(f.account, &own, "wamid."+uuid.NewString(), "text", "Hi", nil, "")
			}()
		}
		wg.Wait()
		f.app.WaitForBackgroundTasks()
		assert.Len(t, f.meta.ofType("text"), 1)
	})

	t.Run("after 7 days it isn't sent", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		held := f.send(t, contact, "See you tomorrow")
		require.NoError(t, f.app.DB.Model(&models.Message{}).Where("id = ?", held.ID).Update("created_at", time.Now().Add(-8*24*time.Hour)).Error)

		f.patientWrites(t, contact, "text", "Hello")

		assert.Empty(t, f.meta.ofType("text"))
		saved := f.status(t, held.ID)
		assert.Equal(t, models.MessageStatusFailed, saved.Status)
		assert.Contains(t, saved.ErrorMessage, "7 days")
	})
}

func TestCancellingHeldMessages(t *testing.T) {
	cancel := func(t *testing.T, f heldFixture, contactID, messageID uuid.UUID) int {
		t.Helper()
		req := testutil.NewJSONRequest(t, map[string]any{})
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		testutil.SetPathParam(req, "id", contactID.String())
		testutil.SetPathParam(req, "message_id", messageID.String())
		require.NoError(t, f.app.CancelHeldMessage(req))
		return testutil.GetResponseStatusCode(req)
	}

	t.Run("a cancelled message never goes out", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		held := f.send(t, contact, "Never mind")

		assert.Equal(t, fasthttp.StatusOK, cancel(t, f, contact.ID, held.ID))

		f.patientWrites(t, contact, "text", "Hello")
		assert.Empty(t, f.meta.ofType("text"))
	})

	t.Run("too late once it has gone out", func(t *testing.T) {
		f := newHeldFixture(t)
		f.configure(t)
		contact := f.contact(t)
		held := f.send(t, contact, "Hello")
		f.patientWrites(t, contact, "text", "Hi")

		assert.Equal(t, fasthttp.StatusConflict, cancel(t, f, contact.ID, held.ID))
	})

	t.Run("can't touch another organization's message", func(t *testing.T) {
		f := newHeldFixture(t)
		other := newHeldFixture(t)
		other.configure(t)
		contact := other.contact(t)
		held := other.send(t, contact, "Private")

		assert.Equal(t, fasthttp.StatusNotFound, cancel(t, f, contact.ID, held.ID))
		assert.Equal(t, models.MessageStatusHeld, other.status(t, held.ID).Status)
	})
}
