package calling

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/shridarpatil/whatomate/pkg/whatsapp"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCallAPI records the call actions sent to Meta.
type fakeCallAPI struct {
	mu         sync.Mutex
	actions    []string
	failAccept bool
}

func (f *fakeCallAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		action, _ := body["action"].(string)
		f.mu.Lock()
		f.actions = append(f.actions, action)
		fail := f.failAccept && action == "accept"
		f.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Call already ended","code":138000}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeCallAPI) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

type ringFixture struct {
	m       *Manager
	api     *fakeCallAPI
	org     *models.Organization
	session *CallSession
	callLog *models.CallLog
	staff   *websocket.Client
}

// newRingFixture is a call that was pre-accepted in "ring" mode: the caller's
// phone is still ringing and staff are being rung for it.
func newRingFixture(t *testing.T, settings models.JSONB) ringFixture {
	t.Helper()
	db := testutil.SetupTestDB(t)
	rd := testutil.SetupTestRedis(t)
	api := &fakeCallAPI{}
	srv := api.server(t)
	hub := websocket.NewHub(testutil.NopLogger())
	go hub.Run()

	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, db.Model(org).Update("settings", settings).Error)
	contact := testutil.CreateTestContact(t, db, org.ID)
	callLog := &models.CallLog{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: "acct",
		ContactID:       contact.ID,
		WhatsAppCallID:  "wacid." + uuid.NewString(),
		CallerPhone:     contact.PhoneNumber,
		Status:          models.CallStatusTransferring,
	}
	require.NoError(t, db.Create(callLog).Error)
	transfer := models.CallTransfer{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		CallLogID:      callLog.ID,
		WhatsAppCallID: callLog.WhatsAppCallID,
		CallerPhone:    contact.PhoneNumber,
		ContactID:      contact.ID,
		Status:         models.CallTransferStatusWaiting,
		TransferredAt:  time.Now(),
	}
	require.NoError(t, db.Create(&transfer).Error)

	m := &Manager{
		sessions: make(map[string]*CallSession),
		log:      testutil.NopLogger(),
		whatsapp: whatsapp.NewWithBaseURL(testutil.NopLogger(), srv.URL),
		db:       db,
		redis:    rd,
		wsHub:    hub,
		config:   &config.CallingConfig{AudioDir: t.TempDir(), HoldMusicFile: "hold.ogg", TransferTimeoutSecs: 60},
	}
	media := make(chan struct{})
	close(media)
	session := &CallSession{
		ID:               callLog.WhatsAppCallID,
		OrganizationID:   org.ID,
		AccountName:      "acct",
		WAAccount:        &whatsapp.Account{PhoneID: "phone-1", APIVersion: "v18.0", AccessToken: "token"},
		ContactID:        contact.ID,
		CallLogID:        callLog.ID,
		Status:           models.CallStatusRinging,
		TransferID:       transfer.ID,
		TransferStatus:   models.CallTransferStatusWaiting,
		AwaitingAccept:   true,
		PendingAnswerSDP: "v=0 answer",
		MediaConnected:   media,
		BridgeStarted:    make(chan struct{}),
	}
	m.sessions[session.ID] = session

	staff := websocket.NewClient(hub, nil, uuid.New(), org.ID)
	hub.Register(staff)
	return ringFixture{m: m, api: api, org: org, session: session, callLog: callLog, staff: staff}
}

func (f ringFixture) reloadLog(t *testing.T) models.CallLog {
	t.Helper()
	var callLog models.CallLog
	require.NoError(t, f.m.db.First(&callLog, f.callLog.ID).Error)
	return callLog
}

func (f ringFixture) transferStatus(t *testing.T) models.CallTransferStatus {
	t.Helper()
	var transfer models.CallTransfer
	require.NoError(t, f.m.db.First(&transfer, f.session.TransferID).Error)
	return transfer.Status
}

func received(client *websocket.Client, kind string) bool {
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case raw := <-client.SendChan():
			var message websocket.WSMessage
			if json.Unmarshal(raw, &message) == nil && message.Type == kind {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func TestIncomingCallSettings(t *testing.T) {
	m := &Manager{config: &config.CallingConfig{AudioDir: "/audio", HoldMusicFile: "hold.ogg"}}

	t.Run("calls are answered straight away unless the clinic chooses otherwise", func(t *testing.T) {
		s := orgCallingSettings{IncomingCallMode: IncomingAnswer, RingNoAnswer: NoAnswerHold}
		m.applyOrgOverrides(&s, map[string]any{})
		assert.Equal(t, IncomingAnswer, s.IncomingCallMode)
		assert.Equal(t, NoAnswerHold, s.RingNoAnswer)

		m.applyOrgOverrides(&s, map[string]any{"incoming_call_mode": "ring", "ring_no_answer": "missed", "waiting_sound": "music"})
		assert.Equal(t, IncomingRing, s.IncomingCallMode)
		assert.Equal(t, NoAnswerMissed, s.RingNoAnswer)
		assert.Equal(t, WaitingMusic, s.WaitingSound)
	})

	t.Run("unknown values are ignored", func(t *testing.T) {
		s := orgCallingSettings{IncomingCallMode: IncomingAnswer, RingNoAnswer: NoAnswerHold}
		m.applyOrgOverrides(&s, map[string]any{"incoming_call_mode": "voicemail", "ring_no_answer": "", "waiting_sound": "jazz"})
		assert.Equal(t, IncomingAnswer, s.IncomingCallMode)
		assert.Equal(t, NoAnswerHold, s.RingNoAnswer)
		assert.Empty(t, s.WaitingSound)
	})

	t.Run("callers hear the chosen sound, or the other one when it isn't uploaded", func(t *testing.T) {
		both := orgCallingSettings{HoldMusicFile: "/audio/hold.ogg", RingbackFile: "/audio/ring.ogg"}
		assert.Equal(t, "/audio/ring.ogg", both.transferRingFile(), "ringing by default")
		both.WaitingSound = WaitingMusic
		assert.Equal(t, "/audio/hold.ogg", both.transferRingFile())
		both.WaitingSound = WaitingRinging
		assert.Equal(t, "/audio/ring.ogg", both.transferRingFile())

		musicOnly := orgCallingSettings{HoldMusicFile: "/audio/hold.ogg", WaitingSound: WaitingRinging}
		assert.Equal(t, "/audio/hold.ogg", musicOnly.transferRingFile())
		ringOnly := orgCallingSettings{RingbackFile: "/audio/ring.ogg", WaitingSound: WaitingMusic}
		assert.Equal(t, "/audio/ring.ogg", ringOnly.transferRingFile())
	})
}

func TestRingingCall(t *testing.T) {
	ring := models.JSONB{"incoming_call_mode": "ring"}

	t.Run("picking up answers the call once, and only then is it answered", func(t *testing.T) {
		f := newRingFixture(t, ring)
		assert.Nil(t, f.reloadLog(t).AnsweredAt, "still ringing")

		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); assert.NoError(t, f.m.answerPending(f.session)) }()
		}
		wg.Wait()

		assert.Equal(t, []string{"accept"}, f.api.sent(), "accepted exactly once")
		assert.False(t, f.session.awaitingAnswer())
		callLog := f.reloadLog(t)
		assert.Equal(t, models.CallStatusAnswered, callLog.Status)
		assert.NotNil(t, callLog.AnsweredAt)
		assert.True(t, received(f.staff, websocket.TypeCallAnswered))
	})

	t.Run("nobody picks up: by default the call is answered and staff keep being rung", func(t *testing.T) {
		f := newRingFixture(t, ring)
		f.m.ringWindowOver(f.session)

		assert.Equal(t, []string{"accept"}, f.api.sent())
		assert.Equal(t, models.CallTransferStatusWaiting, f.transferStatus(t), "still offered to staff")
		assert.NotNil(t, f.reloadLog(t).AnsweredAt)
		assert.NotNil(t, f.m.GetSession(f.session.ID), "the call goes on")
	})

	t.Run("nobody picks up: the clinic can leave it as a missed call", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{"incoming_call_mode": "ring", "ring_no_answer": "missed"})
		f.m.ringWindowOver(f.session)

		assert.Equal(t, []string{"reject"}, f.api.sent(), "declined, never answered")
		assert.Equal(t, models.CallTransferStatusNoAnswer, f.transferStatus(t))
		assert.Nil(t, f.reloadLog(t).AnsweredAt)
		assert.Nil(t, f.m.GetSession(f.session.ID))
		assert.True(t, received(f.staff, websocket.TypeCallTransferNoAnswer), "staff stop ringing")
	})

	t.Run("a call picked up in time is left alone when the window ends", func(t *testing.T) {
		f := newRingFixture(t, ring)
		require.NoError(t, f.m.answerPending(f.session))
		f.m.ringWindowOver(f.session)
		assert.Equal(t, []string{"accept"}, f.api.sent())
	})

	t.Run("a caller who hangs up while it rings isn't declined afterwards", func(t *testing.T) {
		f := newRingFixture(t, ring)
		f.m.HandleCallEvent(f.session.ID, "terminate")
		time.Sleep(200 * time.Millisecond)
		f.m.ringWindowOver(f.session)
		assert.Empty(t, f.api.sent())
		assert.Equal(t, models.CallTransferStatusAbandoned, f.transferStatus(t))
	})

	t.Run("a call that ends unanswered for any other reason is declined, not left ringing", func(t *testing.T) {
		f := newRingFixture(t, ring)
		f.session.mu.Lock()
		f.session.TransferStatus = models.CallTransferStatusNoAnswer
		f.session.mu.Unlock()
		f.m.cleanupSession(f.session.ID)
		assert.Equal(t, []string{"reject"}, f.api.sent())
	})

	t.Run("staff picking up a call that already ended are told so", func(t *testing.T) {
		f := newRingFixture(t, ring)
		f.api.failAccept = true
		_, err := f.m.ConnectAgentToTransfer(f.session.TransferID, uuid.New(), "v=0")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ended before it could be answered")
		assert.Equal(t, []string{"accept", "reject"}, f.api.sent())
		assert.Nil(t, f.m.GetSession(f.session.ID))
	})

	t.Run("a call whose audio WhatsApp already dropped isn't answered into silence", func(t *testing.T) {
		f := newRingFixture(t, ring)
		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		require.NoError(t, err)
		require.NoError(t, pc.Close())
		f.session.mu.Lock()
		f.session.PeerConnection = pc
		f.session.mu.Unlock()

		_, err = f.m.ConnectAgentToTransfer(f.session.TransferID, uuid.New(), "v=0")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ended before it could be answered")
		assert.Equal(t, []string{"reject"}, f.api.sent(), "declined, never accepted")
		assert.Nil(t, f.reloadLog(t).AnsweredAt)
	})

	t.Run("a quick pick-up waits for WhatsApp to confirm the pre-accept, then answers", func(t *testing.T) {
		f := newRingFixture(t, ring)
		preAccepted := make(chan struct{})
		f.session.mu.Lock()
		f.session.PreAccepted = preAccepted
		f.session.mu.Unlock()

		done := make(chan error, 1)
		go func() { done <- f.m.answerPending(f.session) }()
		time.Sleep(150 * time.Millisecond)
		assert.Empty(t, f.api.sent(), "not accepted before the pre-accept is confirmed")

		close(preAccepted)
		require.NoError(t, <-done)
		assert.Equal(t, []string{"accept"}, f.api.sent())
	})

	t.Run("a pre-accept WhatsApp refused is never accepted", func(t *testing.T) {
		f := newRingFixture(t, ring)
		preAccepted := make(chan struct{})
		f.session.mu.Lock()
		f.session.PreAccepted = preAccepted
		f.session.PreAcceptErr = assert.AnError
		f.session.mu.Unlock()
		close(preAccepted)

		require.Error(t, f.m.answerPending(f.session))
		assert.Empty(t, f.api.sent())
	})

	t.Run("staff picking up answers the call before joining it", func(t *testing.T) {
		f := newRingFixture(t, ring)
		// The agent's browser offer is junk here, so joining fails after the
		// answer; what matters is that the caller was answered first.
		_, _ = f.m.ConnectAgentToTransfer(f.session.TransferID, uuid.New(), "v=0")
		sent := f.api.sent()
		require.NotEmpty(t, sent)
		assert.Equal(t, "accept", sent[0])
		assert.NotNil(t, f.reloadLog(t).AnsweredAt)
	})
}
