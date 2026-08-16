package congestion

import (
	"fmt"
	"strings"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/congestion"

	"naivereal/h3frontend/internal/congestion/bbr"
)

// NormalizeType maps a config string to the supported congestion-control name.
// Only BBR is enabled for the H3 frontend; it is the mode we compare with Hysteria2.
func NormalizeType(s string) (string, error) {
	switch strings.ToLower(s) {
	case "", "bbr":
		return "bbr", nil
	default:
		return "", fmt.Errorf("unsupported congestion type %q", s)
	}
}

// UseBBR replaces the connection's congestion controller with the ported
// Hysteria2 BBR implementation.
func UseBBR(conn *quic.Conn, profile string) error {
	p, err := bbr.ParseProfile(profile)
	if err != nil {
		return err
	}
	conn.SetCongestionControl(bbr.NewBbrSender(
		bbr.DefaultClock{},
		seedPacketSize(conn.InitialPacketSize(), bbr.GetInitialPacketSize(conn.RemoteAddr())),
		bbr.Profile(p),
	))
	return nil
}

// seedPacketSize mirrors Hysteria2's seedPacketSize: never seed the replacement
// controller above the size QUIC actually starts at.
func seedPacketSize(quicSize, byAddr congestion.ByteCount) congestion.ByteCount {
	if quicSize <= 0 {
		return byAddr
	}
	return min(quicSize, byAddr)
}
