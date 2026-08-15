package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestXrayClientToOurFrontend validates the reverse interop direction:
// a REAL Xray client (vless + reality outbound) completes the REALITY
// handshake against OUR frontend and gets authenticated (authed counter).
// The vless payload is rejected afterwards (expected - our frontend speaks
// h2 CONNECT), but the handshake + temp cert acceptance prove REALITY
// server-side compatibility. Skipped when xray.exe is unavailable.
func TestXrayClientToOurFrontend(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	xray := findXrayExe(t)
	if xray == "" {
		t.Skip("xray.exe not found near repo (tools/xray-win)")
	}

	// local TLS target site (the fork needs a real TLS 1.3 ServerHello).
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
	frontPort := freePort(t)
	statusPort := freePort(t)

	cfg := &Config{}
	cfg.Inbound.Listen = fmt.Sprintf("127.0.0.1:%d", frontPort)
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
	cfg.Status.HTTP = fmt.Sprintf("127.0.0.1:%d", statusPort)
	cfg.Status.Token = "test"
	srv, err := NewTunnelServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", cfg.Inbound.Listen)
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
	go srv.startStatus(t.Context())

	// xray client config: socks inbound -> vless+reality outbound to our frontend.
	clientPort := freePort(t)
	xcfg := map[string]interface{}{
		"log": map[string]interface{}{"loglevel": "warning"},
		"inbounds": []interface{}{map[string]interface{}{
			"listen": "127.0.0.1", "port": clientPort, "protocol": "socks",
			"settings": map[string]interface{}{"auth": "noauth", "udp": false},
		}},
		"outbounds": []interface{}{map[string]interface{}{
			"protocol": "vless",
			"settings": map[string]interface{}{
				"vnext": []interface{}{map[string]interface{}{
					"address": "127.0.0.1", "port": frontPort,
					"users": []interface{}{map[string]interface{}{
						"id": "00000000-0000-0000-0000-000000000000", "encryption": "none",
					}},
				}},
			},
			"streamSettings": map[string]interface{}{
				"network":  "tcp",
				"security": "reality",
				"realitySettings": map[string]interface{}{
					"show":        false,
					"serverName":  "example.com",
					"fingerprint": "chrome",
					"publicKey":   pubB64,
					"shortId":     "a1b2c3d4e5f60718",
					"spiderX":     "/",
				},
			},
		}},
	}
	data, err := json.MarshalIndent(xcfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	xpath := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(xpath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(xray, "run", "-c", xpath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", clientPort), 15*time.Second)

	// trigger a connection through xray's socks (this dials the reality outbound)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", clientPort), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// SOCKS5 handshake then CONNECT example.com:443 - the vless payload will be
	// rejected by our h2 layer; only the REALITY handshake matters here.
	conn.Write([]byte{5, 1, 0})
	buf := make([]byte, 2)
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.ReadFull(conn, buf)
	conn.Write([]byte{5, 1, 0, 3, 11, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm', 0x01, 0xbb})
	rbuf := make([]byte, 10)
	io.ReadFull(conn, rbuf)

	// poll the frontend stats until the connection was authenticated
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/stats?token=test", statusPort))
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if len(body) > 0 && string(body[0]) == "{" {
				var st map[string]int64
				if json.Unmarshal(body, &st) == nil && st["authed"] >= 1 {
					t.Logf("Xray client authenticated by our REALITY frontend: %s", body)
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("xray client was never authenticated by our frontend")
}
