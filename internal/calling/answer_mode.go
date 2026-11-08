package calling

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
)

// How an organization answers incoming WhatsApp calls (Settings, Calling).
const (
	// IncomingAnswer answers at once, then runs the IVR flow or rings staff
	// while the caller hears the waiting sound. The caller's phone shows the
	// call as answered before anyone has picked up.
	IncomingAnswer = "answer"
	// IncomingRing only pre-accepts, so the caller's phone keeps ringing
	// until a staff member picks up. Meta allows a business only about 30-60
	// seconds to answer, so staff get RingWindow, then RingNoAnswer applies.
	// IVR flows can't play in this mode: nothing reaches the caller before
	// the call is answered.
	IncomingRing = "ring"

	// NoAnswerHold answers when the ring window runs out, plays the waiting
	// sound, and keeps ringing staff until the transfer timeout.
	NoAnswerHold = "hold"
	// NoAnswerMissed declines the call when the ring window runs out; the
	// caller sees it as not answered and the chat logs a missed call.
	NoAnswerMissed = "missed"

	// WaitingRinging plays the ringback tone to a caller waiting for staff.
	WaitingRinging = "ringing"
	// WaitingMusic plays the hold music instead.
	WaitingMusic = "music"
)

// errCallAudioGone: WhatsApp closed the call's audio before it was answered.
var errCallAudioGone = errors.New("the call's audio dropped before it was answered")

// RingWindow is how long a ringing call waits for staff before RingNoAnswer
// applies. It stays inside Meta's answer deadline with room for the accept
// request itself. A variable so tests can shorten it.
var RingWindow = 25 * time.Second

// ValidIncomingMode reports whether v is a known incoming call mode.
func ValidIncomingMode(v string) bool { return v == IncomingAnswer || v == IncomingRing }

// ValidNoAnswer reports whether v is a known no-answer choice.
func ValidNoAnswer(v string) bool { return v == NoAnswerHold || v == NoAnswerMissed }

// ValidWaitingSound reports whether v is a known waiting sound. Empty means
// the default (ringing, falling back to hold music).
func ValidWaitingSound(v string) bool { return v == "" || v == WaitingRinging || v == WaitingMusic }

// IncomingCallMode returns the organization's incoming call mode.
func (m *Manager) IncomingCallMode(orgID uuid.UUID) string {
	return m.getOrgCallingSettings(orgID).IncomingCallMode
}

// awaitingAnswer reports whether the call is still ringing on the caller's
// side: pre-accepted, not yet accepted.
func (s *CallSession) awaitingAnswer() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AwaitingAccept
}

// answerPending accepts a call that was only pre-accepted, once, whoever asks
// first: the staff member picking up, or the ring window running out. The
// call log is marked answered only now, when it really is.
func (m *Manager) answerPending(session *CallSession) error {
	session.acceptMu.Lock()
	defer session.acceptMu.Unlock()

	session.mu.Lock()
	if !session.AwaitingAccept {
		session.mu.Unlock()
		return nil
	}
	sdp := session.PendingAnswerSDP
	account := session.WAAccount
	media := session.MediaConnected
	pc := session.PeerConnection
	preAccepted := session.PreAccepted
	session.mu.Unlock()

	// Staff are rung while WhatsApp confirms the pre-accept; accepting before
	// that confirmation would be refused.
	if preAccepted != nil {
		select {
		case <-preAccepted:
		case <-time.After(10 * time.Second):
			return errors.New("WhatsApp didn't confirm the call in time")
		}
		session.mu.Lock()
		err := session.PreAcceptErr
		session.mu.Unlock()
		if err != nil {
			return errCallAudioGone
		}
	}

	// The caller's audio is already gone: answering would only open a
	// silent line.
	if pc != nil {
		if state := pc.ConnectionState(); state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateFailed {
			return errCallAudioGone
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.whatsapp.AcceptCall(ctx, account, session.ID, sdp); err != nil {
		return err
	}

	now := time.Now()
	session.mu.Lock()
	session.AwaitingAccept = false
	session.Status = models.CallStatusAnswered
	session.mu.Unlock()

	m.db.Model(&models.CallLog{}).Where("id = ?", session.CallLogID).Updates(map[string]any{
		"status":      models.CallStatusAnswered,
		"answered_at": now,
	})
	m.broadcastEvent(session.OrganizationID, websocket.TypeCallAnswered, map[string]any{
		"call_id":     session.ID,
		"contact_id":  session.ContactID.String(),
		"answered_at": now.Format(time.RFC3339),
	})
	m.log.Info("Ringing call answered", "call_id", session.ID)

	// Audio can only flow once the media path is up; it usually is by now,
	// since pre-accepting lets it connect while the phone rings.
	if media != nil {
		select {
		case <-media:
		case <-time.After(10 * time.Second):
			m.log.Warn("Media not connected after answering", "call_id", session.ID)
		}
	}
	return nil
}

// startRingWindow gives staff RingWindow to pick up a ringing call, then
// answers it with the waiting sound or leaves it as a missed call.
func (m *Manager) startRingWindow(session *CallSession) {
	timer := time.NewTimer(RingWindow)
	go func() {
		defer timer.Stop()
		<-timer.C
		m.ringWindowOver(session)
	}()
}

func (m *Manager) ringWindowOver(session *CallSession) {
	if !session.awaitingAnswer() {
		return // picked up, or the caller hung up
	}
	session.mu.Lock()
	transferID := session.TransferID
	waiting := session.TransferStatus == models.CallTransferStatusWaiting
	session.mu.Unlock()

	settings := m.getOrgCallingSettings(session.OrganizationID)
	if waiting && settings.RingNoAnswer != NoAnswerMissed {
		err := m.answerPending(session)
		if err == nil {
			m.playWaitingSound(session, settings)
			m.log.Info("Nobody picked up in the ring window; answered with the waiting sound", "call_id", session.ID)
			return
		}
		m.log.Error("Failed to answer after the ring window", "error", err, "call_id", session.ID)
	}

	m.log.Info("Nobody picked up in the ring window; leaving a missed call", "call_id", session.ID)
	if waiting {
		// Ends the transfer; cleanup declines the still-ringing call.
		m.handleTransferNoAnswer(session, transferID)
		return
	}
	m.cleanupSession(session.ID)
}

// playWaitingSound plays the organization's waiting sound to a caller who is
// waiting for staff, on the player the transfer set up.
func (m *Manager) playWaitingSound(session *CallSession, settings orgCallingSettings) {
	file := settings.transferRingFile()
	session.mu.Lock()
	player := session.HoldPlayer
	session.mu.Unlock()
	if player == nil || file == "" {
		return
	}
	go func() { _ = player.PlayFileLoop(file) }()
}

// declineIfRinging declines a call that was never answered, so the caller's
// phone stops ringing and shows it as not answered.
func (m *Manager) declineIfRinging(session *CallSession) {
	session.mu.Lock()
	pending := session.AwaitingAccept
	session.AwaitingAccept = false
	account := session.WAAccount
	session.mu.Unlock()
	if !pending || account == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.rejectCall(ctx, account, session.ID)
}
