package calling

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
)

// fakePhones records what the calling engine asks of the phones.
type fakePhones struct {
	mu    sync.Mutex
	rings [][]uuid.UUID // nil entry: everyone
	stops []string      // transfer id, "*" suffix when for everyone
}

func (f *fakePhones) RingPhones(_ uuid.UUID, ids []uuid.UUID, _ map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rings = append(f.rings, ids)
}

func (f *fakePhones) StopRinging(_ uuid.UUID, transferID string, ids []uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ids == nil {
		transferID += "*"
	}
	f.stops = append(f.stops, transferID)
}

func (f *fakePhones) snapshot() ([][]uuid.UUID, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]uuid.UUID(nil), f.rings...), append([]string(nil), f.stops...)
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	testutil.AssertEventually(t, ok, 2*time.Second, "phones were told")
}

func TestPhonesFollowCallOffers(t *testing.T) {
	t.Run("a call offered with no calling hours rings everyone's phones", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		phones := &fakePhones{}
		f.m.SetPhoneRinger(phones)
		f.m.offerToRingable(f.org.ID, map[string]any{"id": f.session.TransferID.String()})
		eventually(t, func() bool { rings, _ := phones.snapshot(); return len(rings) == 1 && rings[0] == nil })
	})

	t.Run("a call offered to one person rings only their phones", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		phones := &fakePhones{}
		f.m.SetPhoneRinger(phones)
		agent := uuid.New()
		f.m.offerToUsers(f.org.ID, []uuid.UUID{agent}, map[string]any{"id": f.session.TransferID.String()})
		eventually(t, func() bool {
			rings, _ := phones.snapshot()
			return len(rings) == 1 && len(rings[0]) == 1 && rings[0][0] == agent
		})
	})

	t.Run("nobody answering stops every phone", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		phones := &fakePhones{}
		f.m.SetPhoneRinger(phones)
		f.m.handleTransferNoAnswer(f.session, f.session.TransferID)
		eventually(t, func() bool {
			_, stops := phones.snapshot()
			return len(stops) == 1 && stops[0] == f.session.TransferID.String()+"*"
		})
	})

	t.Run("the caller hanging up stops every phone", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		phones := &fakePhones{}
		f.m.SetPhoneRinger(phones)
		f.m.HandleCallerHangupDuringTransfer(f.session)
		eventually(t, func() bool {
			_, stops := phones.snapshot()
			return len(stops) >= 1 && stops[0] == f.session.TransferID.String()+"*"
		})
	})

	t.Run("someone picking up stops every phone, theirs included", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		phones := &fakePhones{}
		f.m.SetPhoneRinger(phones)
		_, _ = f.m.ConnectAgentToTransfer(f.session.TransferID, uuid.New(), "v=0")
		eventually(t, func() bool {
			_, stops := phones.snapshot()
			return len(stops) >= 1 && stops[0] == f.session.TransferID.String()+"*"
		})
	})

	t.Run("a call moving on from someone stops only their phone", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		phones := &fakePhones{}
		f.m.SetPhoneRinger(phones)
		f.m.stopUserPhones(f.org.ID, f.session.TransferID, uuid.New())
		eventually(t, func() bool {
			_, stops := phones.snapshot()
			return len(stops) == 1 && stops[0] == f.session.TransferID.String()
		})
	})

	t.Run("without a phone ringer, calls still reach browsers", func(t *testing.T) {
		f := newRingFixture(t, models.JSONB{})
		f.m.offerToRingable(f.org.ID, map[string]any{"id": f.session.TransferID.String()})
		f.m.handleTransferNoAnswer(f.session, f.session.TransferID)
	})
}
