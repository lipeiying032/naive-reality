package main

import "testing"

func TestParamNameMapsTheParameterIdsThatMatter(t *testing.T) {
	cases := map[uint64]string{
		0x0:      "original_destination_connection_id",
		0x1:      "max_idle_timeout",
		0x3:      "max_udp_payload_size",
		0x4:      "initial_max_data",
		0xa:      "ack_delay_exponent",
		0xe:      "active_connection_id_limit",
		0x11:     "version_information",
		0x20:     "max_datagram_frame_size",
		0x3127:   "google_initial_rtt",
		0x3128:   "google_connection_options",
	}
	for id, want := range cases {
		if got := paramName(id); got != want {
			t.Errorf("paramName(0x%x) = %q, want %q", id, got, want)
		}
	}
}

// A GREASE parameter id is 31*N+27. It must be reported as such rather than as
// an unknown id, because a fixed id would look like a stable server property
// when it is in fact random per connection.
func TestParamNameRecognisesGrease(t *testing.T) {
	if got := paramName(31*1000 + 27); got != "GREASE(0x7933)" {
		t.Errorf("paramName(GREASE) = %q", got)
	}
	if got := paramName(0x1625); got != "GREASE(0x1625)" {
		t.Errorf("paramName(0x1625) = %q, want the GREASE form", got)
	}
	// 28 is not of the reserved form and must stay unknown.
	if got := paramName(28); got != "unknown(0x1c)" {
		t.Errorf("paramName(28) = %q, want unknown", got)
	}
}

func TestReadVarintDecodesEveryLength(t *testing.T) {
	cases := []struct {
		in   []byte
		want uint64
	}{
		{[]byte{0x00}, 0},
		{[]byte{0x3f}, 63},
		{[]byte{0x40, 0x40}, 64},
		{[]byte{0x7f, 0xff}, 16383},
		{[]byte{0x80, 0x00, 0x40, 0x00}, 16384},
		{[]byte{0x80, 0x00, 0x00, 0x00}, 0},
		{[]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x27, 0x10}, 10000},
	}
	for _, c := range cases {
		got, err := readVarint(c.in)
		if err != nil {
			t.Errorf("readVarint(%x): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("readVarint(%x) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestReadVarintRejectsTruncatedInput(t *testing.T) {
	// 0x40 announces a 2-byte varint but only one byte is present.
	if _, err := readVarint([]byte{0x40}); err == nil {
		t.Error("readVarint accepted a truncated varint")
	}
}

func TestDecodeVarintOnlyAcceptsWholeEncodings(t *testing.T) {
	if v, ok := decodeVarint([]byte{0x40, 0x40}); !ok || v != 64 {
		t.Errorf("decodeVarint(0x4040) = %d, %v; want 64, true", v, ok)
	}
	// Trailing bytes mean this is not a single varint value.
	if _, ok := decodeVarint([]byte{0x40, 0x40, 0x00}); ok {
		t.Error("decodeVarint accepted a value with trailing bytes")
	}
	// The opaque value of initial_source_connection_id must not decode as a number.
	if _, ok := decodeVarint([]byte{0xb2, 0xbd, 0xa7, 0x93, 0xe6, 0x4c, 0xcb, 0x20}); ok {
		t.Error("decodeVarint decoded an opaque connection id as a number")
	}
}
