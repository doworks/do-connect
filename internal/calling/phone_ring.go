package calling

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/websocket"
)

// PhoneRinger rings staff phones (the mobile app) for a call offered to them,
// and stops the ringing once the call is taken or over. The app may be closed,
// so this goes through push notifications, not the live connection.
type PhoneRinger interface {
	// RingPhones rings these users' phones. A nil list means everyone in the
	// organization who may take calls.
	RingPhones(orgID uuid.UUID, userIDs []uuid.UUID, offer map[string]any)
	// StopRinging stops the phones rung for this transfer: these users', or
	// with nil everyone's. A transfer stopped for everyone is over, and must
	// not ring again even if a ring for it is still on its way.
	StopRinging(orgID uuid.UUID, transferID string, userIDs []uuid.UUID)
}

// SetPhoneRinger connects the mobile app's ringing. Without one, calls ring
// only in browsers.
func (m *Manager) SetPhoneRinger(r PhoneRinger) { m.phones = r }

// offerToUsers offers a waiting call to these staff: their open browsers and
// their phones.
func (m *Manager) offerToUsers(orgID uuid.UUID, ids []uuid.UUID, payload map[string]any) {
	if len(ids) == 0 {
		return
	}
	m.noteOffered(payload, ids, false)
	if m.wsHub != nil {
		m.wsHub.BroadcastToUsers(orgID, ids, websocket.WSMessage{
			Type:    websocket.TypeCallTransferWaiting,
			Payload: payload,
		})
	}
	m.ringPhones(orgID, ids, payload)
}

// offerToEveryone offers a waiting call to all staff who may take it.
func (m *Manager) offerToEveryone(orgID uuid.UUID, payload map[string]any) {
	m.noteOffered(payload, nil, true)
	m.broadcastEvent(orgID, websocket.TypeCallTransferWaiting, payload)
	m.ringPhones(orgID, nil, payload)
}

func (m *Manager) ringPhones(orgID uuid.UUID, ids []uuid.UUID, payload map[string]any) {
	if m.phones != nil {
		go m.phones.RingPhones(orgID, ids, payload)
	}
}

// endsTheOffer is every event after which nobody should still be ringing for
// a transfer: someone took it, it was finished, nobody answered, or the
// caller hung up.
var endsTheOffer = map[string]bool{
	websocket.TypeCallTransferConnected: true,
	websocket.TypeCallTransferCompleted: true,
	websocket.TypeCallTransferNoAnswer:  true,
	websocket.TypeCallTransferAbandoned: true,
}

func (m *Manager) stopPhonesFor(orgID uuid.UUID, eventType string, payload map[string]any) {
	if m.phones == nil || !endsTheOffer[eventType] {
		return
	}
	if id, ok := payload["id"].(string); ok && id != "" {
		go m.phones.StopRinging(orgID, id, nil)
	}
}

// stopUserPhones stops one staff member's phones when the call moves on to
// someone else.
func (m *Manager) stopUserPhones(orgID uuid.UUID, transferID uuid.UUID, userID uuid.UUID) {
	if m.phones != nil {
		go m.phones.StopRinging(orgID, transferID.String(), []uuid.UUID{userID})
	}
}
