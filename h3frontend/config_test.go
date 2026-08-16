package main

import (
	"os"
	"path/filepath"
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
	if c.Congestion.Type != "bbr" || c.Congestion.BBRProfile != "standard" {
		t.Fatalf("unexpected congestion config: %+v", c.Congestion)
	}
}
