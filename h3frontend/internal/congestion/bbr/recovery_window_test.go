package bbr

import (
	"testing"
	"time"

	"github.com/apernet/quic-go/congestion"
)

// Regression test for the h3frontend recovery-window floor. Without the floor,
// repeated loss events subtract from recoveryWindow until GetCongestionWindow
// returns minCongestionWindow, permanently pinning a tunnel at a few KB.
func TestCalculateRecoveryWindowKeepsBDPFloor(t *testing.T) {
	b := NewBbrSender(DefaultClock{}, congestion.InitialPacketSize, ProfileStandard)
	b.minRtt = 100 * time.Millisecond
	// 1 Mbit/s bandwidth estimate -> 12.5 KB BDP; the floor should be half.
	b.maxBandwidth.Update(Bandwidth(1_000_000), 0)
	b.isAtFullBandwidth = true
	b.recoveryState = bbrRecoveryStateConservation
	b.recoveryWindow = 0
	b.bytesInFlight = 0

	b.calculateRecoveryWindow(0, 10*congestion.InitialPacketSize)

	floor := b.getTargetCongestionWindow(1) / 2
	if b.recoveryWindow < floor {
		t.Fatalf("recoveryWindow = %d, want at least BDP floor %d", b.recoveryWindow, floor)
	}
	if got := b.GetCongestionWindow(); got < floor {
		t.Fatalf("GetCongestionWindow = %d, want at least floor %d", got, floor)
	}
}
