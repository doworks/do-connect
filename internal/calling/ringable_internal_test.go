package calling

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/callhours"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gotTransfer(client *websocket.Client) bool {
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case raw := <-client.SendChan():
			var message websocket.WSMessage
			if json.Unmarshal(raw, &message) == nil && message.Type == websocket.TypeCallTransferWaiting {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// Waiting calls are offered only to staff the calling hours allow right now.
// Modes that don't depend on the clock keep this test independent of when it runs.
func TestOfferToRingable(t *testing.T) {
	db := testutil.SetupTestDB(t)
	hub := websocket.NewHub(testutil.NopLogger())
	go hub.Run()
	m := &Manager{sessions: map[string]*CallSession{}, log: testutil.NopLogger(), db: db, wsHub: hub}

	org := testutil.CreateTestOrganization(t, db)
	onCall := testutil.CreateTestUser(t, db, org.ID, testutil.WithFullName("On Call"))
	never := testutil.CreateTestUser(t, db, org.ID, testutil.WithFullName("Never"))
	away := testutil.CreateTestUser(t, db, org.ID, testutil.WithFullName("Away"))
	require.NoError(t, db.Model(away).Update("is_available", false).Error)

	clients := map[uuid.UUID]*websocket.Client{}
	for _, user := range []*models.User{onCall, never, away} {
		clients[user.ID] = websocket.NewClient(hub, nil, user.ID, org.ID)
		hub.Register(clients[user.ID])
	}
	testutil.AssertEventually(t, func() bool { return hub.IsUserOnline(org.ID, away.ID) }, 2*time.Second, "signed in")

	t.Run("without calling hours everyone is offered the call", func(t *testing.T) {
		ring := m.ringableNow(org.ID)
		assert.True(t, ring.all)
		m.offerToRingable(org.ID, map[string]any{"id": "t1"})
		for id, client := range clients {
			assert.True(t, gotTransfer(client), id.String())
		}
	})

	rules := callhours.Defaults()
	rules.Staff = []callhours.Staff{
		{ID: onCall.ID.String(), Mode: callhours.ModeAlways},
		{ID: never.ID.String(), Mode: callhours.ModeNever},
		{ID: away.ID.String(), Mode: callhours.ModeAlways},
	}
	require.NoError(t, callhours.Save(db, org.ID, rules))

	t.Run("with calling hours only those on duty", func(t *testing.T) {
		ring := m.ringableNow(org.ID)
		assert.True(t, ring.allows(onCall.ID))
		assert.False(t, ring.allows(never.ID))
		assert.False(t, ring.allows(away.ID), "Away always wins")
		assert.Equal(t, []uuid.UUID{onCall.ID}, ring.filter([]uuid.UUID{never.ID, onCall.ID, away.ID}))

		m.offerToRingable(org.ID, map[string]any{"id": "t2"})
		assert.True(t, gotTransfer(clients[onCall.ID]))
		assert.False(t, gotTransfer(clients[never.ID]))
		assert.False(t, gotTransfer(clients[away.ID]))
	})

	t.Run("close for everyone offers it to nobody", func(t *testing.T) {
		rules.ClosedNow = true
		require.NoError(t, callhours.Save(db, org.ID, rules))
		m.offerToRingable(org.ID, map[string]any{"id": "t3"})
		for _, client := range clients {
			assert.False(t, gotTransfer(client))
		}
	})
}
