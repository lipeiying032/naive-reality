package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// buildTestClientHelloBody assembles a raw TLS 1.3 ClientHello handshake
// message (with the 4-byte header) for SNI "www.apple.com" with a X25519 key
// share. random must be 32 bytes; sessionID nil produces a zero-length
// session_id (QUIC-style).
func buildTestClientHelloBody(random, sessionID, ephPub []byte) []byte {
	return buildTestClientHelloBodySNI(random, sessionID, ephPub, "www.apple.com")
}

func buildTestClientHelloBodySNI(random, sessionID, ephPub []byte, sni string) []byte {
	body := make([]byte, 0, 256)
	body = binary.BigEndian.AppendUint16(body, 0x0303) // legacy_version
	body = append(body, random...)
	if sessionID != nil {
		body = append(body, 0x20) // session_id len 32 (zeroed; sealed later)
		body = append(body, sessionID...)
	} else {
		body = append(body, 0x00)
	}
	body = binary.BigEndian.AppendUint16(body, 2) // cipher_suites len
	body = binary.BigEndian.AppendUint16(body, 0x1301)
	body = append(body, 0x01, 0x00) // compression

	exts := make([]byte, 0, 128)
	// supported_groups
	exts = binary.BigEndian.AppendUint16(exts, 0x000a)
	exts = binary.BigEndian.AppendUint16(exts, 4)
	exts = binary.BigEndian.AppendUint16(exts, 2)
	exts = binary.BigEndian.AppendUint16(exts, 0x001d)
	// key_share: client_shares = group(2) + key_exchange_len(2) + data(32)
	exts = binary.BigEndian.AppendUint16(exts, 0x0033)
	exts = binary.BigEndian.AppendUint16(exts, 2+36)
	exts = binary.BigEndian.AppendUint16(exts, 36)
	exts = binary.BigEndian.AppendUint16(exts, 0x001d)
	exts = binary.BigEndian.AppendUint16(exts, 32)
	exts = append(exts, ephPub...)
	// server_name
	sniBytes := []byte(sni)
	exts = binary.BigEndian.AppendUint16(exts, 0x0000)
	exts = binary.BigEndian.AppendUint16(exts, uint16(5+len(sniBytes)))
	exts = binary.BigEndian.AppendUint16(exts, uint16(3+len(sniBytes)))
	exts = append(exts, 0x00)
	exts = binary.BigEndian.AppendUint16(exts, uint16(len(sniBytes)))
	exts = append(exts, sniBytes...)
	// supported_versions (list length is 1 byte)
	exts = binary.BigEndian.AppendUint16(exts, 0x002b)
	exts = binary.BigEndian.AppendUint16(exts, 3)
	exts = append(exts, 0x02)
	exts = binary.BigEndian.AppendUint16(exts, 0x0304)
	body = binary.BigEndian.AppendUint16(body, uint16(len(exts)))
	body = append(body, exts...)

	msg := make([]byte, 0, 4+len(body))
	msg = append(msg, 0x01) // typeClientHello
	msg = append(msg, byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	msg = append(msg, body...)
	return msg
}

// testClientHello builds a raw TLS 1.3 ClientHello whose random field carries
// the stage-2 REALITY auth payload (mirror of applyRealityClientHelloRandom).
// payload is the 16-byte plaintext (ver3||0||ts||shortId); when nil the
// random stays true random (probe-style, no auth).
func testClientHelloRandom(t *testing.T, ephPriv, serverPub []byte, payload []byte) []byte {
	t.Helper()
	ephPub, err := curve25519.X25519(ephPriv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	if payload == nil {
		return buildTestClientHelloBody(random, nil, ephPub)
	}

	// AD = the full handshake message with the random field zeroed; salt and
	// nonce come from SHA-256(AD), exactly like the fork client.
	msg := buildTestClientHelloBody(make([]byte, 32), nil, ephPub)
	shared, err := curve25519.X25519(ephPriv, serverPub)
	if err != nil {
		t.Fatal(err)
	}
	authKey := make([]byte, 32)
	adHash := sha256.Sum256(msg)
	if _, err = hkdf.New(sha256.New, shared, adHash[:20], []byte("REALITY")).Read(authKey); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(authKey)
	aead, _ := cipher.NewGCM(block)
	cipherText := aead.Seal(nil, adHash[20:32], payload, msg)
	if len(cipherText) != 32 {
		t.Fatalf("random ciphertext is %d bytes, want 32", len(cipherText))
	}

	out := make([]byte, 0, len(msg))
	out = append(out, msg[:6]...)    // header + legacy_version
	out = append(out, cipherText...) // random field
	out = append(out, msg[38:]...)
	return out
}

func appendVarint(dst []byte, v uint64) []byte {
	switch {
	case v < 64:
		return append(dst, byte(v))
	case v < 16384:
		return binary.BigEndian.AppendUint16(dst, uint16(v)|0x4000)
	case v < 1<<30:
		return binary.BigEndian.AppendUint32(dst, uint32(v)|0x80000000)
	default:
		return binary.BigEndian.AppendUint64(dst, v|0xC000000000000000)
	}
}

// testInitialPacket crafts a QUIC v1 Initial datagram carrying a CRYPTO frame
// with the given TLS handshake bytes, using the client-side mirror of
// parseQUICInitial's key schedule.
func testInitialPacket(t *testing.T, payload []byte) []byte {
	t.Helper()
	dcid := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	scid := []byte{0x99, 0xaa, 0xbb, 0xcc}
	key, iv, hp := deriveInitialSecrets(dcid)

	frames := make([]byte, 0, len(payload)+16)
	frames = append(frames, 0x06) // CRYPTO
	frames = appendVarint(frames, 0)
	frames = appendVarint(frames, uint64(len(payload)))
	frames = append(frames, payload...)

	pkt := make([]byte, 0, 128)
	pkt = append(pkt, 0xc0) // long header, Initial, 1-byte PN
	pkt = binary.BigEndian.AppendUint32(pkt, 1)
	pkt = append(pkt, byte(len(dcid)))
	pkt = append(pkt, dcid...)
	pkt = append(pkt, byte(len(scid)))
	pkt = append(pkt, scid...)
	pkt = append(pkt, 0x00) // token len 0
	pkt = appendVarint(pkt, uint64(1+len(frames)+16))
	pnStart := len(pkt)
	pkt = append(pkt, 0x00) // PN = 0

	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, 12)
	copy(nonce, iv)
	cipherText := aead.Seal(nil, nonce, frames, pkt)
	pkt = append(pkt, cipherText...)

	// header protection (sample starts 4 bytes after the PN field start)
	sample := pkt[pnStart+4 : pnStart+4+16]
	blockHP, _ := aes.NewCipher(hp)
	mask := make([]byte, 16)
	blockHP.Encrypt(mask, sample)
	pkt[0] ^= mask[0] & 0x0f
	pkt[pnStart] ^= mask[1]
	return pkt
}

// testInitialPacketOff is testInitialPacket with an explicit CRYPTO stream
// offset, for building fragmented/coalesced ClientHello Initials.
func testInitialPacketOff(t *testing.T, payload []byte, cryptoOff int) []byte {
	t.Helper()
	dcid := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	scid := []byte{0x99, 0xaa, 0xbb, 0xcc}
	key, iv, hp := deriveInitialSecrets(dcid)

	frames := make([]byte, 0, len(payload)+16)
	frames = append(frames, 0x06) // CRYPTO
	frames = appendVarint(frames, uint64(cryptoOff))
	frames = appendVarint(frames, uint64(len(payload)))
	frames = append(frames, payload...)

	pkt := make([]byte, 0, 128)
	pkt = append(pkt, 0xc0) // long header, Initial, 1-byte PN
	pkt = binary.BigEndian.AppendUint32(pkt, 1)
	pkt = append(pkt, byte(len(dcid)))
	pkt = append(pkt, dcid...)
	pkt = append(pkt, byte(len(scid)))
	pkt = append(pkt, scid...)
	pkt = append(pkt, 0x00) // token len 0
	pkt = appendVarint(pkt, uint64(1+len(frames)+16))
	pnStart := len(pkt)
	pkt = append(pkt, 0x00) // PN = 0

	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, 12)
	copy(nonce, iv)
	cipherText := aead.Seal(nil, nonce, frames, pkt)
	pkt = append(pkt, cipherText...)

	// header protection (sample starts 4 bytes after the PN field start)
	sample := pkt[pnStart+4 : pnStart+4+16]
	blockHP, _ := aes.NewCipher(hp)
	mask := make([]byte, 16)
	blockHP.Encrypt(mask, sample)
	pkt[0] ^= mask[0] & 0x0f
	pkt[pnStart] ^= mask[1]
	return pkt
}

func TestParseAllInitialCryptoCoalesced(t *testing.T) {
	// A ClientHello large enough to be fragmented across two coalesced
	// Initial packets (Chromium behaviour with MLKEM hybrid key shares).
	hello := buildTestClientHelloBodySNI(make([]byte, 32), nil, bytes.Repeat([]byte{0x2a}, 32), "www.apple.com")
	split := 40
	part1, part2 := hello[:split], hello[split:]

	datagram := append(testInitialPacketOff(t, part1, 0), testInitialPacketOff(t, part2, split)...)
	frags, parsed := parseAllInitialCrypto(datagram)
	if !parsed {
		t.Fatal("parseAllInitialCrypto did not parse any Initial")
	}
	if len(frags) != 2 {
		t.Fatalf("got %d fragments, want 2", len(frags))
	}
	var buf []byte
	for _, frag := range frags {
		buf = mergeCryptoFrag(buf, frag)
	}
	ch := extractClientHello(buf)
	if ch == nil || !bytes.Equal(ch, hello) {
		t.Fatalf("reassembled ClientHello mismatch: got %d bytes, want %d", len(ch), len(hello))
	}

	// A single-packet datagram must still work (one fragment).
	single := testInitialPacket(t, hello)
	frags2, parsed2 := parseAllInitialCrypto(single)
	if !parsed2 || len(frags2) != 1 {
		t.Fatalf("single-packet parse: parsed=%v frags=%d", parsed2, len(frags2))
	}
}

func TestParseQUICInitialRoundTrip(t *testing.T) {
	hello := []byte{0x01, 0x00, 0x00, 0x03, 0xaa, 0xbb, 0xcc}
	pkt := testInitialPacket(t, hello)
	parsed, err := parseQUICInitial(pkt)
	if err != nil {
		t.Fatalf("parseQUICInitial: %v", err)
	}
	var buf []byte
	for _, frag := range parseCryptoFrames(parsed.Payload) {
		buf = mergeCryptoFrag(buf, frag)
	}
	ch := extractClientHello(buf)
	if ch == nil || !bytes.Equal(ch, hello) {
		t.Fatalf("extractClientHello = %x, want %x", ch, hello)
	}
}

func TestPrecheckPacketConnDecisions(t *testing.T) {
	serverPriv := make([]byte, 32)
	serverPub, _ := curve25519.X25519(serverPriv, curve25519.Basepoint)
	var shortID [8]byte
	copy(shortID[:], []byte{0xde, 0x08, 0x5a, 0xa9, 0, 0, 0, 0})

	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()
	destConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer destConn.Close()

	params := &realityQUICParams{
		PrivateKey:      serverPriv,
		ShortIds:        map[[8]byte]bool{shortID: true},
		ServerNames:     map[string]bool{"www.apple.com": true},
		Dest:            destConn.LocalAddr().String(),
		FallbackTimeout: 5 * time.Second,
	}
	wrapped, err := newRealityPrecheckPacketConn(context.Background(), serverConn, params)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	w := wrapped.(*realityPrecheckPacketConn)

	mkClient := func() (*net.UDPConn, net.Addr) {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c, c.LocalAddr()
	}

	// 1. Probe ClientHello (no auth) -> RELAY to dest, packet kept.
	probeClient, probeAddr := mkClient()
	ephPriv := make([]byte, 32)
	rand.Read(ephPriv)
	probePkt := testInitialPacket(t, testClientHelloRandom(t, ephPriv, serverPub, nil))
	if _, err := probeClient.WriteToUDP(probePkt, serverConn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	destConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := destConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("dest did not receive relayed probe: %v", err)
	}
	if n != len(probePkt) || !bytes.Equal(buf[:n], probePkt) {
		t.Fatalf("relayed probe packet corrupted: got %d bytes, want %d", n, len(probePkt))
	}
	if w.IsAuthenticated(probeAddr) {
		t.Fatal("probe client marked authenticated")
	}

	// 2. Stage-2 ClientHello (auth in the random field) -> AUTH, packet served via ReadFrom.
	authClient, authAddr := mkClient()
	ephPriv2 := make([]byte, 32)
	rand.Read(ephPriv2)
	payload := make([]byte, 16)
	payload[0], payload[1], payload[2] = 26, 4, 17
	binary.BigEndian.PutUint32(payload[4:], uint32(time.Now().Unix()))
	copy(payload[8:], shortID[:])
	authPkt := testInitialPacket(t, testClientHelloRandom(t, ephPriv2, serverPub, payload))
	if _, err := authClient.WriteToUDP(authPkt, serverConn.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}

	type readResult struct {
		n    int
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		rbuf := make([]byte, 2048)
		n, _, err := wrapped.ReadFrom(rbuf)
		data := make([]byte, n)
		copy(data, rbuf[:n])
		ch <- readResult{n, data, err}
	}()
	select {
	case rr := <-ch:
		if rr.err != nil {
			t.Fatalf("ReadFrom: %v", rr.err)
		}
		if rr.n != len(authPkt) || !bytes.Equal(rr.data, authPkt) {
			t.Fatalf("AUTH packet mismatch: got %d bytes, want %d", rr.n, len(authPkt))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFrom did not return AUTH packet")
	}
	if !w.IsAuthenticated(authAddr) {
		t.Fatal("authenticated client not marked AUTH")
	}
}
