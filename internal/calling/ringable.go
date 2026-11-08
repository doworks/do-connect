package calling

import (
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/callhours"
)

// ringable is who the calling hours allow to be rung right now. When the
// organization hasn't set calling hours, all is true and anyone may be.
type ringable struct {
	all bool
	ids map[uuid.UUID]bool
}

func (m *Manager) ringableNow(orgID uuid.UUID) ringable {
	rules, err := callhours.Load(m.db, orgID)
	if err != nil {
		m.log.Error("Failed to load calling hours; anyone may be rung", "error", err, "org_id", orgID)
		return ringable{all: true}
	}
	if !rules.Configured {
		return ringable{all: true}
	}
	ring := ringable{ids: map[uuid.UUID]bool{}}
	for _, id := range rules.RingingStaff(time.Now()) {
		if parsed, err := uuid.Parse(id); err == nil {
			ring.ids[parsed] = true
		}
	}
	return ring
}

func (r ringable) allows(id uuid.UUID) bool {
	return r.all || r.ids[id]
}

func (r ringable) filter(ids []uuid.UUID) []uuid.UUID {
	if r.all {
		return ids
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if r.ids[id] {
			out = append(out, id)
		}
	}
	return out
}

// offerToRingable offers a waiting call to everyone who may take it now.
func (m *Manager) offerToRingable(orgID uuid.UUID, payload map[string]any) {
	ring := m.ringableNow(orgID)
	if ring.all {
		m.offerToEveryone(orgID, payload)
		return
	}
	ids := make([]uuid.UUID, 0, len(ring.ids))
	for id := range ring.ids {
		ids = append(ids, id)
	}
	m.offerToUsers(orgID, ids, payload)
}
