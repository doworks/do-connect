package calling

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
)

type takers struct {
	fakePhones
	ids []uuid.UUID
}

func (t *takers) CallTakers(uuid.UUID) []uuid.UUID { return t.ids }

func TestDecliningACall(t *testing.T) {
	ring := models.JSONB{"incoming_call_mode": "ring"}
	offer := func(f ringFixture) map[string]any { return map[string]any{"id": f.session.TransferID.String()} }
	status := func(f ringFixture) models.CallTransferStatus {
		f.session.mu.Lock()
		defer f.session.mu.Unlock()
		return f.session.TransferStatus
	}

	t.Run("one person declining leaves it ringing for the others", func(t *testing.T) {
		f := newRingFixture(t, ring)
		a, b := uuid.New(), uuid.New()
		f.m.offerToUsers(f.org.ID, []uuid.UUID{a, b}, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, a)
		assert.Equal(t, models.CallTransferStatusWaiting, status(f))
		assert.Empty(t, f.api.sent())
	})

	t.Run("once everyone it was offered to declines, it ends for the caller, never answered", func(t *testing.T) {
		f := newRingFixture(t, ring)
		a, b := uuid.New(), uuid.New()
		f.m.offerToUsers(f.org.ID, []uuid.UUID{a, b}, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, a)
		f.m.DeclineTransfer(f.session.TransferID, b)
		assert.Equal(t, models.CallTransferStatusNoAnswer, status(f))
		assert.Equal(t, []string{"reject"}, f.api.sent())
		assert.Nil(t, f.m.GetSession(f.session.ID))
	})

	t.Run("the only person rung declining ends it", func(t *testing.T) {
		f := newRingFixture(t, ring)
		a := uuid.New()
		f.m.offerToUsers(f.org.ID, []uuid.UUID{a}, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, a)
		assert.Equal(t, []string{"reject"}, f.api.sent())
	})

	t.Run("a call already answered with hold music is hung up when everyone declines", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		f.session.mu.Lock()
		f.session.AwaitingAccept = false
		f.session.mu.Unlock()
		a := uuid.New()
		f.m.offerToUsers(f.org.ID, []uuid.UUID{a}, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, a)
		assert.Equal(t, []string{"terminate"}, f.api.sent())
	})

	t.Run("offered to everyone, it ends once all who may take calls decline", func(t *testing.T) {
		f := newRingFixture(t, ring)
		a, b := uuid.New(), uuid.New()
		f.m.SetPhoneRinger(&takers{ids: []uuid.UUID{a, b}})
		f.m.offerToEveryone(f.org.ID, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, a)
		assert.Equal(t, models.CallTransferStatusWaiting, status(f))
		f.m.DeclineTransfer(f.session.TransferID, b)
		assert.Equal(t, models.CallTransferStatusNoAnswer, status(f))
	})

	t.Run("without knowing who everyone is, declines don't end it", func(t *testing.T) {
		f := newRingFixture(t, ring)
		f.m.offerToEveryone(f.org.ID, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, uuid.New())
		assert.Equal(t, models.CallTransferStatusWaiting, status(f))
	})

	t.Run("in a team rotation a decline doesn't end it; the next person is rung in turn", func(t *testing.T) {
		f := newRingFixture(t, ring)
		f.session.mu.Lock()
		f.session.rotating = true
		f.session.mu.Unlock()
		a := uuid.New()
		f.m.offerToUsers(f.org.ID, []uuid.UUID{a}, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, a)
		assert.Equal(t, models.CallTransferStatusWaiting, status(f))
	})

	t.Run("declining stops the decliner's phones", func(t *testing.T) {
		f := newRingFixture(t, ring)
		phones := &fakePhones{}
		f.m.SetPhoneRinger(phones)
		a, b := uuid.New(), uuid.New()
		f.m.offerToUsers(f.org.ID, []uuid.UUID{a, b}, offer(f))
		f.m.DeclineTransfer(f.session.TransferID, a)
		eventually(t, func() bool {
			_, stops := phones.snapshot()
			for _, s := range stops {
				if s == f.session.TransferID.String() {
					return true
				}
			}
			return false
		})
		time.Sleep(50 * time.Millisecond)
	})
}
