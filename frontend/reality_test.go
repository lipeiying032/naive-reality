package main

import (
	"bytes"
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// TestRealityRelayToTarget verifies that an unauthenticated TLS client
// (a prober) gets its ClientHello relayed to the configured target and
// completes the handshake with the TARGET's certificate.
func TestRealityRelayToTarget(t *testing.T) {
	// 1. fake target: a TLS server with its own certificate that echoes.
	targetCert, _, _ := makeCert(t)
	targetLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{targetCert},
	})
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
				tc := c.(*tls.Conn)
				tc.SetDeadline(time.Now().Add(10 * time.Second))
				if err := tc.Handshake(); err != nil {
					return
				}
				buf := make([]byte, 64)
				for {
					n, err := tc.Read(buf)
					if err != nil {
						return
					}
					if _, err := tc.Write(bytes.ToUpper(buf[:n])); err != nil {
						return
					}
				}
			}(c)
		}
	}()

	// 2. frontend in reality mode relaying to the fake target.
	privB64, _, err := generateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	cfg.Inbound.Listen = "127.0.0.1:0"
	cfg.Inbound.Mode = "reality"
	cfg.Inbound.Reality.PrivateKey = privB64
	cfg.Inbound.Reality.ShortIDs = []string{"abcd"}
	cfg.Inbound.Reality.ServerNames = []string{"example.com"}
	cfg.Inbound.Reality.Target = targetLn.Addr().String()
	cfg.Upstream.Addr = "127.0.0.1:1" // unused in this test
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

	// 3. prober: plain TLS client with a random session id (not authed).
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		ServerName:         "example.com",
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
	})
	if err != nil {
		t.Fatalf("prober handshake should succeed via relay: %v", err)
	}
	defer conn.Close()
	peers := conn.ConnectionState().PeerCertificates
	if len(peers) == 0 {
		t.Fatal("no peer cert presented")
	}
	if !bytes.Equal(peers[0].Raw, targetCert.Certificate[0]) {
		t.Error("prober received a certificate that is NOT the target site's certificate")
	}
	if _, err := conn.Write([]byte("probe")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	if _, err := conn.Read(got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "PROBE" {
		t.Errorf("relay echo = %q, want PROBE", got)
	}
}
