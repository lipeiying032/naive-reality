package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"naivereal/frontend/internal/realitytest"
)

// TestXrayRealityInterop validates REALITY-layer compatibility with the
// reference Xray-core server: our realitytest client must complete the
// handshake against a real Xray VLESS+Reality inbound and verify its
// temporary certificate. Skipped when xray.exe is not available.
func TestXrayRealityInterop(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	xray := findXrayExe(t)
	if xray == "" {
		t.Skip("xray.exe not found near repo (tools/xray-win)")
	}

	// local TLS 1.3 target site (xray relays unauth traffic to it and uses
	// its ServerHello as the handshake template).
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
	port := freePort(t)

	cfg := map[string]interface{}{
		"log": map[string]interface{}{"loglevel": "warning"},
		"inbounds": []interface{}{map[string]interface{}{
			"listen":   "127.0.0.1",
			"port":     port,
			"protocol": "vless",
			"settings": map[string]interface{}{
				"clients":    []interface{}{map[string]interface{}{"id": "00000000-0000-0000-0000-000000000000"}},
				"decryption": "none",
			},
			"streamSettings": map[string]interface{}{
				"network":  "tcp",
				"security": "reality",
				"realitySettings": map[string]interface{}{
					"show":        false,
					"dest":        targetLn.Addr().String(),
					"xver":        0,
					"serverNames": []string{"example.com"},
					"privateKey":  privB64,
					"shortIds":    []string{"a1b2c3d4e5f60718"},
				},
			},
		}},
		"outbounds": []interface{}{map[string]interface{}{"protocol": "freedom"}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(xray, "run", "-c", cfgPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", port), 15*time.Second)

	var serverPub [32]byte
	copy(serverPub[:], pubBytes)
	var shortID [8]byte
	copy(shortID[:], []byte{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18})

	rc, err := realitytest.Dial(fmt.Sprintf("127.0.0.1:%d", port), "example.com", serverPub, shortID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if err := rc.Handshake(); err != nil {
		t.Fatalf("REALITY handshake against Xray failed: %v", err)
	}
	if !rc.Verified {
		t.Fatal("Xray server temporary certificate was not verified (HMAC check failed)")
	}
	t.Logf("Xray REALITY interop OK: handshake completed, temp cert verified (authKey[0:4]=%x)", rc.AuthKey[:4])
}

// findXrayExe locates xray.exe near the repo.
func findXrayExe(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		for _, pat := range []string{
			filepath.Join(dir, "tools", "xray-win", "xray.exe"),
			filepath.Join(dir, "tools", "xray-win", "*", "xray.exe"),
		} {
			matches, _ := filepath.Glob(pat)
			if len(matches) > 0 {
				return matches[0]
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func waitPort(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("port %s never came up", addr)
}
