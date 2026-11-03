package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// receiver stands in for the integration at the other end of a webhook.
type receiver struct {
	server  *httptest.Server
	status  atomic.Int32
	mu      sync.Mutex
	bodies  []string
	headers []http.Header
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	rec := &receiver{}
	rec.status.Store(200)
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, string(body))
		rec.headers = append(rec.headers, r.Header.Clone())
		rec.mu.Unlock()
		w.WriteHeader(int(rec.status.Load()))
	}))
	t.Cleanup(rec.server.Close)
	return rec
}

func (rec *receiver) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.bodies)
}

func (f heldFixture) webhook(t *testing.T, rec *receiver, secret string, events ...string) *models.Webhook {
	t.Helper()
	webhook := &models.Webhook{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: f.org.ID,
		Name:           "Clinic system",
		URL:            rec.server.URL,
		Events:         events,
		Secret:         secret,
		IsActive:       true,
	}
	require.NoError(t, f.app.DB.Create(webhook).Error)
	return webhook
}

func (f heldFixture) deliveries(t *testing.T, webhookID uuid.UUID) []models.WebhookDelivery {
	t.Helper()
	var rows []models.WebhookDelivery
	require.NoError(t, f.app.DB.Where("webhook_id = ?", webhookID).Order("created_at ASC").Find(&rows).Error)
	return rows
}

func (f heldFixture) makeDue(t *testing.T, id uuid.UUID) {
	t.Helper()
	require.NoError(t, f.app.DB.Model(&models.WebhookDelivery{}).Where("id = ?", id).Update("next_attempt_at", time.Now().Add(-time.Second)).Error)
}

func TestWebhookDeliveries(t *testing.T) {
	event := models.WebhookEventMessageIncoming

	t.Run("delivered once and recorded", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		webhook := f.webhook(t, rec, "", string(event))

		f.app.DispatchWebhook(f.org.ID, event, map[string]string{"hello": "there"})
		f.app.WaitForBackgroundTasks()

		assert.Equal(t, 1, rec.count())
		rows := f.deliveries(t, webhook.ID)
		require.Len(t, rows, 1)
		assert.Equal(t, models.WebhookDeliveryDelivered, rows[0].Status)
		assert.NotNil(t, rows[0].DeliveredAt)
	})

	t.Run("an integration that was down gets it when it's back", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		rec.status.Store(503)
		webhook := f.webhook(t, rec, "", string(event))

		f.app.DispatchWebhook(f.org.ID, event, map[string]string{"answer": "Yes"})
		f.app.WaitForBackgroundTasks()

		rows := f.deliveries(t, webhook.ID)
		require.Len(t, rows, 1)
		assert.Equal(t, models.WebhookDeliveryPending, rows[0].Status)
		assert.Equal(t, 503, rows[0].LastStatusCode)
		assert.Contains(t, rows[0].LastError, "503")
		require.NotNil(t, rows[0].NextAttemptAt)
		assert.True(t, rows[0].NextAttemptAt.After(time.Now()))

		assert.Equal(t, quickDeliveryTries, rec.count(), "a blip gets quick retries")

		// Not due yet, so nothing more is tried.
		f.app.ProcessDueDeliveries(context.Background())
		assert.Equal(t, quickDeliveryTries, rec.count())

		rec.status.Store(200)
		f.makeDue(t, rows[0].ID)
		f.app.ProcessDueDeliveries(context.Background())

		rows = f.deliveries(t, webhook.ID)
		assert.Equal(t, models.WebhookDeliveryDelivered, rows[0].Status)
		assert.Equal(t, quickDeliveryTries+1, rows[0].Attempts)
		assert.Contains(t, rec.bodies[quickDeliveryTries], "Yes")
	})

	t.Run("it gives up after the last retry and says why", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		rec.status.Store(500)
		webhook := f.webhook(t, rec, "", string(event))
		f.app.DispatchWebhook(f.org.ID, event, map[string]string{"a": "b"})
		f.app.WaitForBackgroundTasks()

		for range deliveryBackoff {
			rows := f.deliveries(t, webhook.ID)
			f.makeDue(t, rows[0].ID)
			f.app.ProcessDueDeliveries(context.Background())
		}
		rows := f.deliveries(t, webhook.ID)
		assert.Equal(t, models.WebhookDeliveryFailed, rows[0].Status)
		assert.Equal(t, quickDeliveryTries+len(deliveryBackoff), rows[0].Attempts)
		assert.Nil(t, rows[0].NextAttemptAt)
		assert.Contains(t, rows[0].LastError, "500")
	})

	t.Run("every delivery is signed, including after the webhook list is cached", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		f.webhook(t, rec, "the-secret", string(event))

		for range 2 {
			f.app.DispatchWebhook(f.org.ID, event, map[string]string{"a": "b"})
			f.app.WaitForBackgroundTasks()
		}
		require.Equal(t, 2, rec.count())
		for i, body := range rec.bodies {
			mac := hmac.New(sha256.New, []byte("the-secret"))
			mac.Write([]byte(body))
			assert.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), rec.headers[i].Get("X-Webhook-Signature"), "delivery %d", i)
		}
	})

	t.Run("several servers don't send the same retry twice", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		rec.status.Store(503)
		webhook := f.webhook(t, rec, "", string(event))
		f.app.DispatchWebhook(f.org.ID, event, map[string]string{"a": "b"})
		f.app.WaitForBackgroundTasks()
		rows := f.deliveries(t, webhook.ID)
		rec.status.Store(200)
		f.makeDue(t, rows[0].ID)

		var wg sync.WaitGroup
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.app.ProcessDueDeliveries(context.Background())
			}()
		}
		wg.Wait()
		assert.Equal(t, quickDeliveryTries+1, rec.count())
	})

	t.Run("the log shows failures, not patient messages, and can resend", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		rec.status.Store(403)
		webhook := f.webhook(t, rec, "", string(event))
		f.app.DispatchWebhook(f.org.ID, event, map[string]string{"text": "private patient words"})
		f.app.WaitForBackgroundTasks()

		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		testutil.SetPathParam(req, "id", webhook.ID.String())
		require.NoError(t, f.app.ListWebhookDeliveries(req))
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
		body := string(testutil.GetResponseBody(req))
		assert.Contains(t, body, "403")
		assert.NotContains(t, body, "private patient words")

		rows := f.deliveries(t, webhook.ID)
		resend := func() int {
			r := testutil.NewJSONRequest(t, map[string]any{})
			testutil.SetAuthContext(r, f.org.ID, f.user.ID)
			testutil.SetPathParam(r, "id", webhook.ID.String())
			testutil.SetPathParam(r, "delivery_id", rows[0].ID.String())
			require.NoError(t, f.app.ResendWebhookDelivery(r))
			return testutil.GetResponseStatusCode(r)
		}
		assert.Equal(t, fasthttp.StatusBadGateway, resend())
		rec.status.Store(200)
		assert.Equal(t, fasthttp.StatusOK, resend())
		assert.Equal(t, models.WebhookDeliveryDelivered, f.deliveries(t, webhook.ID)[0].Status)
	})

	t.Run("another organization can't see or resend them", func(t *testing.T) {
		f := newHeldFixture(t)
		other := newHeldFixture(t)
		rec := newReceiver(t)
		rec.status.Store(500)
		webhook := other.webhook(t, rec, "", string(event))
		other.app.DispatchWebhook(other.org.ID, event, map[string]string{"a": "b"})
		other.app.WaitForBackgroundTasks()

		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		testutil.SetPathParam(req, "id", webhook.ID.String())
		require.NoError(t, f.app.ListWebhookDeliveries(req))
		assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
	})
}

func TestMessageStatusEvents(t *testing.T) {
	statusEvent := func(t *testing.T, rec *receiver, index int) map[string]any {
		t.Helper()
		var payload struct {
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		require.Greater(t, rec.count(), index)
		require.NoError(t, json.Unmarshal([]byte(rec.bodies[index]), &payload))
		assert.Equal(t, "message.status", payload.Event)
		return payload.Data
	}
	outgoing := func(t *testing.T, f heldFixture, contact *models.Contact, wamid string) *models.Message {
		t.Helper()
		message := &models.Message{
			BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: f.org.ID, WhatsAppAccount: f.account.Name, ContactID: contact.ID,
			Direction: models.DirectionOutgoing, MessageType: models.MessageTypeTemplate, TemplateName: "reminder", Content: "x",
			Status: models.MessageStatusSent, WhatsAppMessageID: wamid,
		}
		require.NoError(t, f.app.DB.Create(message).Error)
		return message
	}

	t.Run("a failed reminder reaches the integration with Meta's reason", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		f.webhook(t, rec, "", string(models.WebhookEventMessageStatus))
		contact := f.contact(t)
		message := outgoing(t, f, contact, "wamid.fail")

		f.app.updateMessageStatus("wamid.fail", "failed", []WebhookStatusError{{Code: 131026, Title: "Undeliverable", Message: "Not on WhatsApp"}})
		f.app.WaitForBackgroundTasks()

		data := statusEvent(t, rec, 0)
		assert.Equal(t, message.ID.String(), data["message_id"])
		assert.Equal(t, "failed", data["status"])
		assert.Equal(t, float64(131026), data["error_code"])
		assert.Equal(t, "Not on WhatsApp", data["error_message"])
		assert.Equal(t, "reminder", data["template_name"])
	})

	t.Run("delivered and read are reported too, with times saved", func(t *testing.T) {
		f := newHeldFixture(t)
		rec := newReceiver(t)
		f.webhook(t, rec, "", string(models.WebhookEventMessageStatus))
		contact := f.contact(t)
		message := outgoing(t, f, contact, "wamid.ok")

		f.app.updateMessageStatus("wamid.ok", "delivered", nil)
		f.app.WaitForBackgroundTasks()
		f.app.updateMessageStatus("wamid.ok", "read", nil)
		f.app.WaitForBackgroundTasks()

		assert.Equal(t, "delivered", statusEvent(t, rec, 0)["status"])
		assert.Equal(t, "read", statusEvent(t, rec, 1)["status"])
		var saved models.Message
		require.NoError(t, f.app.DB.First(&saved, message.ID).Error)
		assert.NotNil(t, saved.DeliveredAt)
		assert.NotNil(t, saved.ReadAt)
	})
}

func webhookBody(phoneID, messageID, text string) []byte {
	body, _ := json.Marshal(map[string]any{"object": "whatsapp_business_account", "entry": []any{map[string]any{"id": "biz", "changes": []any{map[string]any{"field": "messages", "value": map[string]any{
		"messaging_product": "whatsapp",
		"metadata":          map[string]any{"phone_number_id": phoneID},
		"contacts":          []any{map[string]any{"profile": map[string]any{"name": "Patient"}, "wa_id": "97333112233"}},
		"messages":          []any{map[string]any{"from": "97333112233", "id": messageID, "timestamp": "1", "type": "text", "text": map[string]any{"body": text}}},
	}}}}}})
	return body
}

func TestIncomingMessages(t *testing.T) {
	post := func(t *testing.T, f heldFixture, body []byte, signature string) int {
		t.Helper()
		req := testutil.NewJSONRequest(t, map[string]any{})
		req.RequestCtx.Request.SetBody(body)
		if signature != "" {
			req.RequestCtx.Request.Header.Set("X-Hub-Signature-256", signature)
		}
		require.NoError(t, f.app.WebhookHandler(req))
		return testutil.GetResponseStatusCode(req)
	}
	waitForMessage := func(t *testing.T, f heldFixture, id string) {
		t.Helper()
		assert.Eventually(t, func() bool {
			var row models.IncomingWebhook
			return f.app.DB.Where("whats_app_message_id = ? AND processed_at IS NOT NULL", id).First(&row).Error == nil
		}, 5*time.Second, 50*time.Millisecond)
	}
	sign := func(body []byte, secret string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	t.Run("a message is saved, processed and closed", func(t *testing.T) {
		f := newHeldFixture(t)
		id := "wamid." + uuid.NewString()
		assert.Equal(t, fasthttp.StatusOK, post(t, f, webhookBody(f.account.PhoneID, id, "Hello"), ""))
		waitForMessage(t, f, id)
		var count int64
		f.app.DB.Model(&models.Message{}).Where("whats_app_message_id = ?", id).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("a message stranded by a crash is picked up again", func(t *testing.T) {
		f := newHeldFixture(t)
		id := "wamid." + uuid.NewString()
		row, err := f.app.queueIncomingMessage(f.account.PhoneID, IncomingTextMessage{ID: id, From: "97333112233", Type: "text", Text: &struct {
			Body string `json:"body"`
		}{Body: "Did this arrive?"}}, "Patient")
		require.NoError(t, err)

		// Too recent: the first attempt may still be running.
		f.app.ReprocessIncomingMessages(context.Background())
		var untouched models.IncomingWebhook
		require.NoError(t, f.app.DB.First(&untouched, row.ID).Error)
		assert.Zero(t, untouched.Attempts)
		require.NoError(t, f.app.DB.Model(row).UpdateColumn("updated_at", time.Now().Add(-time.Hour)).Error)

		f.app.ReprocessIncomingMessages(context.Background())
		f.app.WaitForBackgroundTasks()

		var saved models.Message
		require.NoError(t, f.app.DB.Where("whats_app_message_id = ?", id).First(&saved).Error)
		assert.Equal(t, "Did this arrive?", saved.Content)
		var closed models.IncomingWebhook
		require.NoError(t, f.app.DB.First(&closed, row.ID).Error)
		assert.NotNil(t, closed.ProcessedAt)
	})

	t.Run("the same message delivered at once is saved once", func(t *testing.T) {
		f := newHeldFixture(t)
		id := "wamid." + uuid.NewString()
		body := webhookBody(f.account.PhoneID, id, "Hello")
		var wg sync.WaitGroup
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				post(t, f, body, "")
			}()
		}
		wg.Wait()
		waitForMessage(t, f, id)
		time.Sleep(300 * time.Millisecond)
		var count int64
		f.app.DB.Model(&models.Message{}).Where("whats_app_message_id = ?", id).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("a number with an App Secret only accepts signed messages", func(t *testing.T) {
		f := newHeldFixture(t)
		require.NoError(t, f.app.DB.Model(f.account).Update("app_secret", "meta-secret").Error)
		f.app.InvalidateWhatsAppAccountCache(f.account.PhoneID)
		id := "wamid." + uuid.NewString()
		body := webhookBody(f.account.PhoneID, id, "Forged")

		assert.Equal(t, fasthttp.StatusForbidden, post(t, f, body, ""), "no signature")
		assert.Equal(t, fasthttp.StatusForbidden, post(t, f, body, "sha256=00"), "wrong signature")
		var count int64
		f.app.DB.Model(&models.IncomingWebhook{}).Where("whats_app_message_id = ?", id).Count(&count)
		assert.Zero(t, count)

		assert.Equal(t, fasthttp.StatusOK, post(t, f, body, sign(body, "meta-secret")), "signed by Meta")
		waitForMessage(t, f, id)
	})
}

func TestAPISendsReportTheResult(t *testing.T) {
	send := func(t *testing.T, f heldFixture, body map[string]any, viaKey bool) (int, MessageResponse, string) {
		t.Helper()
		req := testutil.NewJSONRequest(t, body)
		testutil.SetAuthContext(req, f.org.ID, f.user.ID)
		if viaKey {
			req.RequestCtx.SetUserValue(middleware.ContextKeyViaAPIKey, true)
		}
		require.NoError(t, f.app.SendTemplateMessage(req))
		var resp struct {
			Data    MessageResponse `json:"data"`
			Message string          `json:"message"`
		}
		_ = json.Unmarshal(testutil.GetResponseBody(req), &resp)
		return testutil.GetResponseStatusCode(req), resp.Data, resp.Message
	}

	t.Run("an integration gets the WhatsApp message ID", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "reminder", "en", "Yes")
		code, data, _ := send(t, f, map[string]any{"contact_id": f.contact(t).ID.String(), "template_name": "reminder", "sent_by": "Automatic reminder"}, true)
		assert.Equal(t, fasthttp.StatusOK, code)
		assert.Equal(t, models.MessageStatusSent, data.Status)
		assert.NotEmpty(t, data.WAMID)
		assert.Equal(t, "Automatic reminder", data.SentByLabel)
	})

	t.Run("an integration is told when Meta refuses", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "reminder", "en", "Yes")
		f.meta.fail("Template paused by Meta")
		code, _, message := send(t, f, map[string]any{"contact_id": f.contact(t).ID.String(), "template_name": "reminder"}, true)
		assert.Equal(t, fasthttp.StatusBadGateway, code)
		assert.Contains(t, message, "Template paused by Meta")
	})

	t.Run("a refused send shows as failed in the chat list", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "reminder", "en", "Yes")
		contact := f.contact(t)
		f.meta.fail("Template paused by Meta")
		send(t, f, map[string]any{"contact_id": contact.ID.String(), "template_name": "reminder"}, true)
		var saved models.Contact
		require.NoError(t, f.app.DB.First(&saved, contact.ID).Error)
		assert.True(t, saved.LastSendFailed)
	})

	t.Run("the chat list shows the template's text, not its name", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "reminder", "en", "Yes")
		contact := f.contact(t)
		send(t, f, map[string]any{"contact_id": contact.ID.String(), "template_name": "reminder"}, true)
		var saved models.Contact
		require.NoError(t, f.app.DB.First(&saved, contact.ID).Error)
		assert.Equal(t, "You have a message.", saved.LastMessagePreview)
	})

	t.Run("staff can't claim to be someone else", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "reminder", "en", "Yes")
		_, data, _ := send(t, f, map[string]any{"contact_id": f.contact(t).ID.String(), "template_name": "reminder", "sent_by": "Dr Someone"}, false)
		assert.Empty(t, data.SentByLabel)
		assert.Equal(t, "Noor Ali", data.SentByName)
	})

	t.Run("the language picks between templates sharing a name", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "reminder", "en", "Yes")
		f.template(t, "reminder", "ar", "نعم")
		contact := f.contact(t)
		send(t, f, map[string]any{"contact_id": contact.ID.String(), "template_name": "reminder", "language": "ar"}, true)
		sent := f.meta.ofType("template")
		require.Len(t, sent, 1)
		assert.Equal(t, "ar", sent[0]["template"].(map[string]any)["language"].(map[string]any)["code"])
	})

	t.Run("a template missing its required file is refused with the reason", func(t *testing.T) {
		f := newHeldFixture(t)
		f.template(t, "invoice", "en", "Pay")
		require.NoError(t, f.app.DB.Model(&models.Template{}).Where("name = ?", "invoice").Update("header_type", "DOCUMENT").Error)
		code, _, message := send(t, f, map[string]any{"contact_id": f.contact(t).ID.String(), "template_name": "invoice"}, true)
		assert.Equal(t, fasthttp.StatusBadRequest, code)
		assert.Contains(t, message, "document")
		assert.Empty(t, f.meta.ofType("template"))
	})
}

func TestStuckSendsAndCleanup(t *testing.T) {
	f := newHeldFixture(t)
	contact := f.contact(t)
	stuck := &models.Message{BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: f.org.ID, WhatsAppAccount: f.account.Name, ContactID: contact.ID, Direction: models.DirectionOutgoing, MessageType: models.MessageTypeText, Content: "x", Status: models.MessageStatusPending}
	recent := &models.Message{BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: f.org.ID, WhatsAppAccount: f.account.Name, ContactID: contact.ID, Direction: models.DirectionOutgoing, MessageType: models.MessageTypeText, Content: "y", Status: models.MessageStatusPending}
	require.NoError(t, f.app.DB.Create(stuck).Error)
	require.NoError(t, f.app.DB.Create(recent).Error)
	require.NoError(t, f.app.DB.Model(stuck).UpdateColumn("updated_at", time.Now().Add(-time.Hour)).Error)

	f.app.failStuckSends()

	assert.Equal(t, models.MessageStatusFailed, f.status(t, stuck.ID).Status)
	assert.Contains(t, f.status(t, stuck.ID).ErrorMessage, "restarted")
	assert.Equal(t, models.MessageStatusPending, f.status(t, recent.ID).Status, "still within a normal send")

	old := &models.WebhookDelivery{BaseModel: models.BaseModel{ID: uuid.New(), CreatedAt: time.Now().Add(-8 * 24 * time.Hour)}, OrganizationID: f.org.ID, WebhookID: uuid.New(), Event: "x", Payload: "patient words", Status: models.WebhookDeliveryDelivered}
	require.NoError(t, f.app.DB.Create(old).Error)
	f.app.cleanUpOldRows()
	var count int64
	f.app.DB.Unscoped().Model(&models.WebhookDelivery{}).Where("id = ?", old.ID).Count(&count)
	assert.Zero(t, count, "old patient data isn't kept")
}

func TestFailedDownloadsCanBeRetried(t *testing.T) {
	f := newHeldFixture(t)
	contact := f.contact(t)
	message := &models.Message{BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: f.org.ID, WhatsAppAccount: f.account.Name, ContactID: contact.ID, Direction: models.DirectionIncoming, MessageType: models.MessageTypeImage, MediaMimeType: "image/jpeg", Status: models.MessageStatusDelivered, Metadata: models.JSONB{"media_id": "media-1"}}
	require.NoError(t, f.app.DB.Create(message).Error)

	f.meta.fail("Media not ready")
	require.Error(t, f.app.downloadMessageMedia(context.Background(), message))
	var saved models.Message
	require.NoError(t, f.app.DB.First(&saved, message.ID).Error)
	assert.Equal(t, float64(1), saved.Metadata["media_attempts"], "the failure is counted")
	assert.Equal(t, "media-1", saved.Metadata["media_id"], "and it can be tried again")

	// The background retry finds it too.
	f.app.RetryFailedMediaDownloads(context.Background())
	require.NoError(t, f.app.DB.First(&saved, message.ID).Error)
	assert.Equal(t, float64(2), saved.Metadata["media_attempts"])

	req := testutil.NewJSONRequest(t, map[string]any{})
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	testutil.SetPathParam(req, "id", uuid.NewString())
	testutil.SetPathParam(req, "message_id", message.ID.String())
	require.NoError(t, f.app.RetryMessageMedia(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

func TestDeletingAKeyOwnerIsRefused(t *testing.T) {
	f := newHeldFixture(t)
	role := testutil.CreateAdminRole(t, f.app.DB, f.org.ID)
	owner := testutil.CreateTestUser(t, f.app.DB, f.org.ID, testutil.WithRoleID(&role.ID))
	require.NoError(t, f.app.DB.Create(&models.APIKey{BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: f.org.ID, UserID: owner.ID, Name: "Clinic system", KeyPrefix: uuid.NewString()[:8], KeyHash: "x", IsActive: true}).Error)

	req := testutil.NewJSONRequest(t, map[string]any{})
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	testutil.SetPathParam(req, "id", owner.ID.String())
	require.NoError(t, f.app.DeleteUser(req))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(req))
	assert.Contains(t, string(testutil.GetResponseBody(req)), "API keys")
}

func TestCustomActionPlaceholders(t *testing.T) {
	out := replaceVariables("https://clinic.test/?name={{contact.metadata.whatsapp_name}}", map[string]any{
		"contact": map[string]any{"metadata": models.JSONB{"whatsapp_name": "fatoom"}},
	})
	assert.Equal(t, "https://clinic.test/?name=fatoom", out)
}
