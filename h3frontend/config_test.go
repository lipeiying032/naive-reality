package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	for _, p := range []string{cert, key} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "h3frontend.toml")
	cfg := `
listen = "127.0.0.1:0"
[tls]
cert = "` + cert + `"
key = "` + key + `"
[upstream]
addr = "127.0.0.1:18080"
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.QUIC.InitialStreamReceiveWindow != 8*1024*1024 ||
		c.QUIC.MaxStreamReceiveWindow != 8*1024*1024 ||
		c.QUIC.InitialConnectionReceiveWindow != 20*1024*1024 ||
		c.QUIC.MaxConnectionReceiveWindow != 20*1024*1024 {
		t.Fatalf("unexpected default QUIC windows: %+v", c.QUIC)
	}
	if c.QUIC.InitialPacketSize != 1200 {
		t.Fatalf("unexpected default initial packet size: %d", c.QUIC.InitialPacketSize)
	}
	if c.Congestion.Type != "bbr" || c.Congestion.BBRProfile != "standard" {
		t.Fatalf("unexpected congestion config: %+v", c.Congestion)
	}
	if c.Mode != "tls" {
		t.Fatalf("default mode should be tls, got %q", c.Mode)
	}
}

const testRealityPrivKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 32 zero bytes, base64url

func TestRealityModeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h3frontend.toml")
	cfg := `
listen = "127.0.0.1:0"
mode = "reality"
[reality]
private_key = "` + testRealityPrivKey + `"
short_ids = ["0123456789abcdef", ""]
server_names = ["www.microsoft.com", "www.apple.com"]
dest = "www.microsoft.com:443"
fallback_timeout = "60s"
[upstream]
addr = "127.0.0.1:18080"
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != "reality" {
		t.Fatalf("mode = %q, want reality", c.Mode)
	}
	if len(c.Reality.ServerNames) != 2 || c.Reality.ServerNames[0] != "www.microsoft.com" {
		t.Fatalf("unexpected server_names: %+v", c.Reality.ServerNames)
	}
	if c.Reality.DestServerName != "www.microsoft.com" {
		t.Fatalf("dest_server_name default = %q, want server_names[0]", c.Reality.DestServerName)
	}
	if c.Reality.FallbackTimeout != "60s" {
		t.Fatalf("fallback_timeout = %q", c.Reality.FallbackTimeout)
	}
	if _, err := parseRealityPrivateKey(c.Reality.PrivateKey); err != nil {
		t.Fatal(err)
	}
	if _, err := parseShortIDs(c.Reality.ShortIDs); err != nil {
		t.Fatal(err)
	}
}

func TestRealityModeValidationErrors(t *testing.T) {
	write := func(t *testing.T, body string) string {
		path := filepath.Join(t.TempDir(), "h3frontend.toml")
		cfg := `
listen = "127.0.0.1:0"
` + body + `
[upstream]
addr = "127.0.0.1:18080"
`
		if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	priv := "private_key = \"" + testRealityPrivKey + "\"\n"
	mode := "mode = \"reality\"\n"
	cases := []struct {
		name string
		body string
		want string
	}{
		{"missing private key", mode + "[reality]\nshort_ids = [\"01\"]\nserver_names = [\"a.com\"]\ndest = \"a.com:443\"\n", "reality.private_key"},
		{"bad private key", mode + "[reality]\nprivate_key = \"bm90LWhleA\"\nshort_ids = [\"01\"]\nserver_names = [\"a.com\"]\ndest = \"a.com:443\"\n", "reality.private_key"},
		{"missing short ids", mode + "[reality]\n" + priv + "server_names = [\"a.com\"]\ndest = \"a.com:443\"\n", "reality.short_ids"},
		{"bad short id", mode + "[reality]\n" + priv + "short_ids = [\"zzzz\"]\nserver_names = [\"a.com\"]\ndest = \"a.com:443\"\n", "reality.short_ids"},
		{"missing server names", mode + "[reality]\n" + priv + "short_ids = [\"01\"]\ndest = \"a.com:443\"\n", "reality.server_names"},
		{"missing dest", mode + "[reality]\n" + priv + "short_ids = [\"01\"]\nserver_names = [\"a.com\"]\n", "reality.dest"},
		{"bad mode", "mode = \"badmode\"\n", "mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig(write(t, tc.body))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestRealityH3CertPairValidation(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(cert, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "h3frontend.toml")
	cfg := `
listen = "127.0.0.1:0"
mode = "reality"
[reality]
private_key = "` + testRealityPrivKey + `"
short_ids = ["01"]
server_names = ["a.com"]
dest = "a.com:443"
h3_cert = "` + cert + `"
[upstream]
addr = "127.0.0.1:18080"
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "h3_key") {
		t.Fatalf("expected h3_cert/h3_key pair error, got %v", err)
	}
}
