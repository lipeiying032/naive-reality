package tun

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

// buildQuery makes a minimal A-record query for name.
func buildQuery(t *testing.T, name string, qtype uint16) []byte {
	t.Helper()
	var b bytes.Buffer
	var hdr [12]byte
	binary.BigEndian.PutUint16(hdr[0:], 0x1234)
	binary.BigEndian.PutUint16(hdr[2:], flagRD)
	binary.BigEndian.PutUint16(hdr[4:], 1)
	b.Write(hdr[:])
	labels := bytes.Split([]byte(name), []byte("."))
	for _, l := range labels {
		b.WriteByte(byte(len(l)))
		b.Write(l)
	}
	b.WriteByte(0)
	var q [4]byte
	binary.BigEndian.PutUint16(q[0:], qtype)
	binary.BigEndian.PutUint16(q[2:], 1) // class IN
	b.Write(q[:])
	return b.Bytes()
}

func TestParseQuery(t *testing.T) {
	q := buildQuery(t, "www.example.com", qtypeA)
	h, name, qt, err := parseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if name != "www.example.com" || qt != qtypeA || h.ID != 0x1234 {
		t.Errorf("parsed: %q %d %x", name, qt, h.ID)
	}
}

func TestBuildResponseRoundtrip(t *testing.T) {
	q := buildQuery(t, "example.com", qtypeA)
	resp, err := buildResponse(q, []net.IP{net.ParseIP("93.184.216.34")}, 300)
	if err != nil {
		t.Fatal(err)
	}
	ips, err := parseAnswers(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("93.184.216.34")) {
		t.Errorf("answers: %v", ips)
	}
	// NXDOMAIN case
	resp2, err := buildResponse(q, nil, 300)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseAnswers(resp2); err == nil {
		t.Error("NXDOMAIN response should have no answers")
	}
}

func TestBuildResponseDropsEDNSBeforeAnswers(t *testing.T) {
	q := buildQuery(t, "example.com", qtypeA)
	// Add a root-name OPT pseudo-record and advertise it in ARCOUNT.
	binary.BigEndian.PutUint16(q[10:], 1)
	q = append(q, 0, 0, 41, 0x10, 0, 0, 0, 0, 0, 0)
	resp, err := buildResponse(q, []net.IP{net.ParseIP("93.184.216.34")}, 300)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint16(resp[10:]); got != 0 {
		t.Fatalf("ARCOUNT = %d, want 0", got)
	}
	ips, err := parseAnswers(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("93.184.216.34")) {
		t.Fatalf("answers: %v", ips)
	}
}

func TestParseQueryRejectsMultipleQuestions(t *testing.T) {
	q := buildQuery(t, "example.com", qtypeA)
	binary.BigEndian.PutUint16(q[4:], 2)
	if _, _, _, err := parseQuery(q); err == nil {
		t.Fatal("multiple questions should be rejected")
	}
}

func TestParseAnswersAAAA(t *testing.T) {
	resp, err := buildResponse(buildQuery(t, "example.com", qtypeAAAA), []net.IP{net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")}, 60)
	if err != nil {
		t.Fatal(err)
	}
	ips, err := parseAnswers(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || ips[0].String() != "2606:2800:220:1:248:1893:25c8:1946" {
		t.Errorf("answers: %v", ips)
	}
}
