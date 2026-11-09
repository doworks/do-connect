package calling

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
)

// CallTakerLister lists who may take calls in an organization. The phone
// ringer provides it, so a call offered to everyone knows who "everyone" is.
type CallTakerLister interface {
	CallTakers(orgID uuid.UUID) []uuid.UUID
}

// noteOffered remembers who a waiting call was offered to, so that once all
// of them have declined it can end instead of ringing on for nobody.
func (m *Manager) noteOffered(payload map[string]any, ids []uuid.UUID, everyone bool) {
	raw, _ := payload["id"].(string)
	transferID, err := uuid.Parse(raw)
	if err != nil {
		return
	}
	session := m.findSessionByTransferID(transferID)
	if session == nil {
		return
	}
	if everyone {
		lister, ok := m.phones.(CallTakerLister)
		if !ok {
			// Unknown who "everyone" is: declines never end the call; it
			// rings until the ring window or transfer timeout.
			session.mu.Lock()
			session.offeredUnknown = true
			session.mu.Unlock()
			return
		}
		ids = lister.CallTakers(session.OrganizationID)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.offered == nil {
		session.offered = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		session.offered[id] = true
	}
}

// DeclineTransfer is one staff member saying they won't take a waiting call.
// Their phones and browsers stop ringing; the call carries on for anyone else
// it was offered to. Once everyone it was offered to has declined, it ends
// for the caller as a missed call. In a team rotation the next person is rung
// when this one's turn runs out, as before. An organization can instead have
// any one decline end the call (Settings, Calling), e.g. when a single phone
// takes all calls.
func (m *Manager) DeclineTransfer(transferID, userID uuid.UUID) {
	session := m.findSessionByTransferID(transferID)
	if session == nil {
		return
	}
	session.mu.Lock()
	if session.TransferStatus != models.CallTransferStatusWaiting || session.TransferID != transferID {
		session.mu.Unlock()
		return
	}
	if session.declined == nil {
		session.declined = map[uuid.UUID]bool{}
	}
	session.declined[userID] = true
	everyoneDeclined := !session.rotating && !session.offeredUnknown && len(session.offered) > 0
	for id := range session.offered {
		if !session.declined[id] {
			everyoneDeclined = false
		}
	}
	orgID := session.OrganizationID
	inIVR := session.TransferDone != nil
	session.mu.Unlock()
	if m.getOrgCallingSettings(orgID).DeclineEndsCall {
		everyoneDeclined = true
	}

	if m.wsHub != nil {
		m.wsHub.BroadcastToUser(orgID, userID, websocket.WSMessage{
			Type:    websocket.TypeCallTransferReassigned,
			Payload: map[string]any{"id": transferID.String(), "reason": "declined"},
		})
	}
	m.stopUserPhones(orgID, transferID, userID)
	if !everyoneDeclined {
		return
	}

	m.log.Info("Everyone the call was offered to declined it; ending it", "call_id", session.ID, "transfer_id", transferID)
	answered := !session.awaitingAnswer()
	m.handleTransferNoAnswer(session, transferID)
	// A call already answered (the caller on hold) is hung up; one still
	// ringing was declined by the cleanup. An IVR flow takes over on its own.
	if answered && !inIVR {
		m.terminateCallBySession(session)
	}
}
