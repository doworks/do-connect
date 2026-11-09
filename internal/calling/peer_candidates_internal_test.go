package calling

import (
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatheredCandidates builds a call connection, offers audio and returns the
// candidate lines it would give WhatsApp.
func gatheredCandidates(t *testing.T, cfg *config.CallingConfig) []string {
	t.Helper()
	m := &Manager{config: cfg, log: testutil.NopLogger()}
	pc, err := m.createPeerConnection()
	require.NoError(t, err)
	defer func() { _ = pc.Close() }()
	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio)
	require.NoError(t, err)
	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	done := webrtc.GatheringCompletePromise(pc)
	require.NoError(t, pc.SetLocalDescription(offer))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("candidate gathering didn't finish")
	}
	var lines []string
	for _, line := range strings.Split(pc.LocalDescription().SDP, "\n") {
		if strings.HasPrefix(line, "a=candidate:") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

func TestCallsOfferWhatsAppOnePathWhenThePublicAddressIsKnown(t *testing.T) {
	cfg := &config.CallingConfig{
		PublicIP:   "203.0.113.7",
		UDPPortMin: 41000,
		UDPPortMax: 41100,
		// Unreachable on purpose: with the public address known it must not be asked.
		ICEServers: []config.ICEServerConfig{{URLs: []string{"stun:192.0.2.1:3478"}}},
	}
	lines := gatheredCandidates(t, cfg)
	require.NotEmpty(t, lines)
	for _, line := range lines {
		assert.Contains(t, line, "203.0.113.7", "only the public address")
		assert.Contains(t, line, "typ host", "no STUN candidate for the same address")
		assert.Contains(t, line, " udp ", "UDP only")
		assert.NotContains(t, line, "::", "no IPv6")
	}
}

func TestNoSTUNServerWhenThePublicAddressIsKnown(t *testing.T) {
	servers := func(cfg *config.CallingConfig) []webrtc.ICEServer {
		m := &Manager{config: cfg, log: testutil.NopLogger()}
		pc, err := m.createPeerConnection()
		require.NoError(t, err)
		defer func() { _ = pc.Close() }()
		return pc.GetConfiguration().ICEServers
	}
	both := []config.ICEServerConfig{
		{URLs: []string{"stun:stun.example:3478"}},
		{URLs: []string{"stun:turn.example:3478", "turn:turn.example:3478"}, Username: "u", Credential: "p"},
	}
	known := servers(&config.CallingConfig{PublicIP: "203.0.113.7", ICEServers: both})
	require.Len(t, known, 1, "the STUN-only entry is dropped")
	assert.Equal(t, []string{"turn:turn.example:3478"}, known[0].URLs, "TURN stays")

	unknown := servers(&config.CallingConfig{ICEServers: both})
	assert.Len(t, unknown, 2, "without a public address STUN is how it's found")
}

func TestTURNIsKeptWhenThePublicAddressIsKnown(t *testing.T) {
	assert.Equal(t, []string{"turn:turn.example:3478?transport=udp"},
		withoutSTUN([]string{"stun:stun.example:3478", "STUNS:stun.example:5349", "turn:turn.example:3478?transport=udp"}))
	assert.Empty(t, withoutSTUN([]string{"stun:stun.l.google.com:19302"}))
}
