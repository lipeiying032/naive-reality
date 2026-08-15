//go:build windows

package tun

import (
	"net"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip/header"
)

func TestInboundPacketUsesReportedByteLength(t *testing.T) {
	buf := make([]byte, 128)
	buf[0] = 0x45
	data, protocol, ok := inboundPacket(buf, 64)
	if !ok {
		t.Fatal("valid IPv4 packet rejected")
	}
	if len(data) != 64 {
		t.Fatalf("packet length = %d, want 64", len(data))
	}
	if protocol != header.IPv4ProtocolNumber {
		t.Fatalf("protocol = %d, want IPv4", protocol)
	}
}

func TestCreateRejectsGatewayOutsideSubnetBeforeOpeningDevice(t *testing.T) {
	_, err := Create(Config{
		Gateway: "192.0.2.1",
		Subnet:  "198.18.0.0/15",
		Dial: func(string, string) (net.Conn, error) {
			return nil, nil
		},
	})
	if err == nil {
		t.Fatal("gateway outside subnet should fail")
	}
}

func TestInboundPacketRejectsInvalidLengthAndVersion(t *testing.T) {
	if _, _, ok := inboundPacket([]byte{0x45}, 2); ok {
		t.Fatal("oversized packet accepted")
	}
	if _, _, ok := inboundPacket([]byte{0x70}, 1); ok {
		t.Fatal("unknown IP version accepted")
	}
}
