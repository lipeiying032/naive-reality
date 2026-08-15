package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"naivereal/frontend/internal/realitytest"
)

// TestRealityAuthedTunnel: correct short id -> temp cert verified -> h2 tunnel.
func TestRealityAuthedTunnel(t *testing.T) {
	upstream, echo := startMockUpstream(t)
	// The fork pre-dials the target for EVERY connection and, on the authed
	// path, uses the TARGET's ServerHello as the template for its own
	// handshake. The target must therefore be a working TLS 1.3 server.
	targetCert, _, _ := makeCert(t)
	targetLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{targetCert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { targetLn.Close() })
	go func() {
		for {
			c, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.(*tls.Conn).SetDeadline(time.Now().Add(30 * time.Second))
				c.(*tls.Conn).Handshake()
			}(c)
		}
	}()
	privB64, pubB64, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	pubBytes := decodeB64(t, pubB64)

	cfg := &Config{}
	cfg.Inbound.Listen = "127.0.0.1:0"
	cfg.Inbound.Mode = "reality"
	cfg.Inbound.Reality.PrivateKey = privB64
	cfg.Inbound.Reality.ShortIDs = []string{"a1b2c3d4e5f60718"}
	cfg.Inbound.Reality.ServerNames = []string{"example.com"}
	cfg.Inbound.Reality.Target = targetLn.Addr().String()
	cfg.Upstream.Addr = upstream
	cfg.Limits.MaxConnections = 16
	cfg.Limits.MaxRelays = 8
	cfg.Limits.HandshakeTimeout.Duration = 10 * time.Second
	cfg.Limits.IdleTimeout.Duration = 60 * time.Second
	srv, err := NewTunnelServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(c)
		}
	}()

	var serverPub [32]byte
	copy(serverPub[:], pubBytes)
	var shortID [8]byte
	copy(shortID[:], []byte{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18})

	rc, err := realitytest.Dial(ln.Addr().String(), "example.com", serverPub, shortID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if err := rc.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if !rc.Verified {
		t.Fatal("temporary certificate was not verified")
	}
	hc := newTestH2Client(t, rc.UConn)
	resp := hc.connect(t, "example.com:443", http.Header{
		"proxy-authorization":  []string{"Basic dXNlcjpwYXNz"},
		"padding":              []string{"~~~~"},
		"padding-type-request": []string{"1, 0"},
	})
	if resp.status != 200 {
		t.Fatalf("connect status = %d", resp.status)
	}
	if resp.header.Get("padding-type-reply") != "1" {
		t.Errorf("padding-type-reply = %q", resp.header.Get("padding-type-reply"))
	}
	buf := make([]byte, len(echo))
	if _, err := io.ReadFull(hc.body(), buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(echo) {
		t.Errorf("echo = %q, want %q", buf, echo)
	}
	if _, err := hc.body().Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(hc.body(), got); err != nil {
		t.Fatalf("read echo2: %v", err)
	}
	if string(got) != "ping" {
		t.Errorf("echo2 = %q", got)
	}
}

// TestRealityWrongShortIDRelayed: wrong short id -> server relays us to the
// target; we must receive the target's certificate, not a temp cert.
func TestRealityWrongShortIDRelayed(t *testing.T) {
	targetCert, _, _ := makeCert(t)
	targetLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{targetCert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { targetLn.Close() })
	go func() {
		for {
			c, err := targetLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.(*tls.Conn).Handshake()
			}(c)
		}
	}()

	privB64, pubB64, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	pubBytes := decodeB64(t, pubB64)
	cfg := &Config{}
	cfg.Inbound.Listen = "127.0.0.1:0"
	cfg.Inbound.Mode = "reality"
	cfg.Inbound.Reality.PrivateKey = privB64
	cfg.Inbound.Reality.ShortIDs = []string{"a1b2c3d4e5f60718"}
	cfg.Inbound.Reality.ServerNames = []string{"example.com"}
	cfg.Inbound.Reality.Target = targetLn.Addr().String()
	cfg.Upstream.Addr = "127.0.0.1:1"
	cfg.Limits.MaxConnections = 16
	cfg.Limits.MaxRelays = 8
	cfg.Limits.HandshakeTimeout.Duration = 10 * time.Second
	cfg.Limits.IdleTimeout.Duration = 60 * time.Second
	srv, err := NewTunnelServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(c)
		}
	}()

	var serverPub [32]byte
	copy(serverPub[:], pubBytes)
	var wrongShortID [8]byte // all zeros: not in ShortIDs set

	rc, err := realitytest.Dial(ln.Addr().String(), "example.com", serverPub, wrongShortID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	rc.Handshake() // expected to fail or complete against the target
	if rc.Verified {
		t.Fatal("wrong short id must not yield a verified temp cert")
	}
	if rc.PresentedLeaf == nil {
		t.Fatal("no certificate presented")
	}
	if !bytes.Equal(rc.PresentedLeaf, targetCert.Certificate[0]) {
		t.Error("presented cert is not the relay target's certificate")
	}
}

// decodeB64 decodes a base64url string into bytes, failing the test on error.
func decodeB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}
