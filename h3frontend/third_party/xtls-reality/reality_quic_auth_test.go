package reality

import (
	"bytes"
	"testing"
)

func TestExtractClientKeySharePrefersX25519(t *testing.T) {
	x25519Pub := bytes.Repeat([]byte{0x11}, 32)
	hybridPub := bytes.Repeat([]byte{0x22}, 32)
	// hybrid share = MLKEM ct (simulated) + trailing X25519 component.
	hybridData := append(bytes.Repeat([]byte{0x33}, 1088), hybridPub...)

	// Hybrid listed first (Chromium order), plain X25519 second.
	hello := &clientHelloMsg{keyShares: []keyShare{
		{group: X25519MLKEM768, data: hybridData},
		{group: X25519, data: x25519Pub},
	}}
	if got := extractClientKeyShare(hello); !bytes.Equal(got, x25519Pub) {
		t.Fatalf("extractClientKeyShare with hybrid-first = %x, want plain X25519 %x", got, x25519Pub)
	}

	// X25519 listed first.
	hello2 := &clientHelloMsg{keyShares: []keyShare{
		{group: X25519, data: x25519Pub},
		{group: X25519MLKEM768, data: hybridData},
	}}
	if got := extractClientKeyShare(hello2); !bytes.Equal(got, x25519Pub) {
		t.Fatalf("extractClientKeyShare with x25519-first = %x, want plain X25519 %x", got, x25519Pub)
	}

	// Only hybrid share: fall back to its trailing X25519 component.
	hello3 := &clientHelloMsg{keyShares: []keyShare{
		{group: X25519MLKEM768, data: hybridData},
	}}
	if got := extractClientKeyShare(hello3); !bytes.Equal(got, hybridPub) {
		t.Fatalf("extractClientKeyShare with only hybrid = %x, want trailing X25519 %x", got, hybridPub)
	}

	// No usable share.
	hello4 := &clientHelloMsg{keyShares: nil}
	if got := extractClientKeyShare(hello4); got != nil {
		t.Fatalf("extractClientKeyShare with no shares = %x, want nil", got)
	}
}
