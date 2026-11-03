package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func (f heldFixture) received(t *testing.T, contact *models.Contact, kind models.MessageType, content, mediaURL string) *models.Message {
	t.Helper()
	message := &models.Message{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  f.org.ID,
		WhatsAppAccount: f.account.Name,
		ContactID:       contact.ID,
		Direction:       models.DirectionIncoming,
		MessageType:     kind,
		Content:         content,
		MediaURL:        mediaURL,
		MediaMimeType:   "image/jpeg",
		MediaFilename:   "xray.jpg",
		Status:          models.MessageStatusReceived,
	}
	require.NoError(t, f.app.DB.Create(message).Error)
	return message
}

func (f heldFixture) forward(t *testing.T, messageID uuid.UUID, targets ...uuid.UUID) (int, []ForwardResult) {
	t.Helper()
	req := testutil.NewJSONRequest(t, map[string]any{"contact_ids": targets})
	testutil.SetAuthContext(req, f.org.ID, f.user.ID)
	testutil.SetPathParam(req, "message_id", messageID.String())
	require.NoError(t, f.app.ForwardMessage(req))
	f.app.WaitForBackgroundTasks()
	var out struct {
		Data struct {
			Results []ForwardResult `json:"results"`
		} `json:"data"`
	}
	_ = json.Unmarshal(testutil.GetResponseBody(req), &out)
	return testutil.GetResponseStatusCode(req), out.Data.Results
}

func TestForwarding(t *testing.T) {
	t.Run("a text goes to every chosen chat, marked forwarded and named", func(t *testing.T) {
		f := newHeldFixture(t)
		source := f.received(t, f.contact(t, recentlyWrote), models.MessageTypeText, "Please send the referral to Dr Amal", "")
		a := f.contact(t, recentlyWrote)
		b := f.contact(t, recentlyWrote)

		status, results := f.forward(t, source.ID, a.ID, b.ID, a.ID)
		require.Equal(t, fasthttp.StatusOK, status)
		require.Len(t, results, 2, "the same chat twice is sent once")
		for _, result := range results {
			assert.Equal(t, "sent", result.Status, result.Reason)
			copy := f.status(t, result.MessageID)
			assert.Equal(t, "Please send the referral to Dr Amal", copy.Content)
			assert.Equal(t, source.ID.String(), copy.Metadata["forwarded_from"])
			assert.Equal(t, f.user.ID, *copy.SentByUserID)
		}
		assert.Len(t, f.meta.ofType("text"), 2)
	})

	t.Run("outside the window a text waits for the reply, if that's set up", func(t *testing.T) {
		f := newHeldFixture(t)
		source := f.received(t, f.contact(t, recentlyWrote), models.MessageTypeText, "Your results are ready", "")
		quiet := f.contact(t)

		_, results := f.forward(t, source.ID, quiet.ID)
		assert.Equal(t, "failed", results[0].Status)
		assert.Contains(t, results[0].Reason, "24 hours")

		f.configure(t)
		_, results = f.forward(t, source.ID, quiet.ID)
		assert.Equal(t, "held", results[0].Status)
		assert.Equal(t, models.MessageStatusHeld, f.status(t, results[0].MessageID).Status)
		assert.Len(t, f.meta.ofType("template"), 1, "the unlock notice went out")
		assert.Empty(t, f.meta.ofType("text"))
	})

	t.Run("a photo is sent from the saved file, but not outside the window", func(t *testing.T) {
		f := newHeldFixture(t)
		f.app.Config.Storage.LocalPath = t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(f.app.Config.Storage.LocalPath, "xray.jpg"), []byte("jpeg-bytes"), 0o600))
		source := f.received(t, f.contact(t, recentlyWrote), models.MessageTypeImage, "Lower left molar", "xray.jpg")
		open := f.contact(t, recentlyWrote)
		quiet := f.contact(t)

		_, results := f.forward(t, source.ID, open.ID, quiet.ID)
		require.Len(t, results, 2)
		assert.Equal(t, "sent", results[0].Status, results[0].Reason)
		images := f.meta.ofType("image")
		require.Len(t, images, 1)
		assert.Equal(t, "Lower left molar", images[0]["image"].(map[string]any)["caption"])
		assert.Equal(t, "failed", results[1].Status)
		assert.Contains(t, results[1].Reason, "Photos and files")
	})

	t.Run("what can't be forwarded says why", func(t *testing.T) {
		f := newHeldFixture(t)
		contact := f.contact(t, recentlyWrote)
		target := f.contact(t, recentlyWrote)

		missing := f.received(t, contact, models.MessageTypeImage, "", "")
		status, _ := f.forward(t, missing.ID, target.ID)
		assert.Equal(t, fasthttp.StatusUnprocessableEntity, status, "a photo that never downloaded")

		reaction := f.received(t, contact, models.MessageTypeReaction, "👍", "")
		status, _ = f.forward(t, reaction.ID, target.ID)
		assert.Equal(t, fasthttp.StatusUnprocessableEntity, status)

		f.configure(t)
		held := f.send(t, f.contact(t), "Not sent yet")
		status, _ = f.forward(t, held.ID, target.ID)
		assert.Equal(t, fasthttp.StatusUnprocessableEntity, status)

		many := make([]uuid.UUID, 11)
		for i := range many {
			many[i] = uuid.New()
		}
		source := f.received(t, contact, models.MessageTypeText, "hi", "")
		status, _ = f.forward(t, source.ID, many...)
		assert.Equal(t, fasthttp.StatusBadRequest, status)
		assert.Empty(t, f.meta.ofType("text"))
	})

	t.Run("a chat in another organisation isn't reachable", func(t *testing.T) {
		f := newHeldFixture(t)
		other := newHeldFixture(t)
		source := f.received(t, f.contact(t, recentlyWrote), models.MessageTypeText, "hi", "")
		_, results := f.forward(t, source.ID, other.contact(t, recentlyWrote).ID)
		assert.Equal(t, "failed", results[0].Status)
		assert.Equal(t, "Chat not found", results[0].Reason)
	})
}
