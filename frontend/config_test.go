package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = "log_level = \"info\"\n[inbound]\nlisten = \"0.0.0.0:443\"\nmode = \"reality\"\n[inbound.reality]\nprivate_key = \"mOkR9JS7u0vfLCUQjf6kE_NOWJvNf4VCNYb9A1wK_Ek\"\nshort_ids = [\"ab12cd34ef56\", \"\"]\nserver_names = [\"www.example.com\"]\ntarget = \"www.example.com:443\"\n[upstream]\naddr = \"127.0.0.1:8080\"\n"

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frontend.toml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inbound.Listen != "0.0.0.0:443" {
		t.Errorf("listen = %q", cfg.Inbound.Listen)
	}
	if cfg.Inbound.Mode != "reality" {
		t.Errorf("mode = %q", cfg.Inbound.Mode)
	}
	if cfg.Inbound.Reality.Target != "www.example.com:443" {
		t.Errorf("target = %q", cfg.Inbound.Reality.Target)
	}
	if cfg.Upstream.Addr != "127.0.0.1:8080" {
		t.Errorf("upstream = %q", cfg.Upstream.Addr)
	}
	if cfg.Limits.MaxConnections != 1024 {
		t.Errorf("max_connections default = %d", cfg.Limits.MaxConnections)
	}
	ids, err := parseShortIDs(cfg.Inbound.Reality.ShortIDs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("short ids parsed = %d", len(ids))
	}
	var zero [8]byte
	if !ids[zero] {
		t.Error("empty short id should map to zero key")
	}
}

func TestConfigValidationErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := write("no_key.toml", "[inbound]\nmode = \"reality\"\n")
	if _, err := LoadConfig(p); err == nil {
		t.Error("missing private_key should fail")
	}
	p = write("bad_sid.toml", "[inbound]\nmode = \"reality\"\n[inbound.reality]\nprivate_key = \"mOkR9JS7u0vfLCUQjf6kE_NOWJvNf4VCNYb9A1wK_Ek\"\nserver_names = [\"x.com\"]\nshort_ids = [\"zz\"]\n")
	if _, err := LoadConfig(p); err == nil {
		t.Error("invalid short id should fail")
	}
	p = write("bad_key.toml", "[inbound]\nmode = \"reality\"\n[inbound.reality]\nprivate_key = \"abc\"\nserver_names = [\"x.com\"]\n")
	if _, err := LoadConfig(p); err == nil {
		t.Error("short private key should fail")
	}
	p = write("bad_mode.toml", "[inbound]\nmode = \"bogus\"\n")
	if _, err := LoadConfig(p); err == nil {
		t.Error("bogus mode should fail")
	}
}

func TestConfigRejectsUnsafeOrAmbiguousSettings(t *testing.T) {
	dir := t.TempDir()
	validReality := "[inbound]\nmode = \"reality\"\n[inbound.reality]\nprivate_key = \"mOkR9JS7u0vfLCUQjf6kE_NOWJvNf4VCNYb9A1wK_Ek\"\nserver_names = [\"x.com\"]\nshort_ids = [\"abcd\"]\n"
	tests := []struct {
		name string
		body string
		want string
	}{
		{"missing short IDs", strings.Replace(validReality, "short_ids = [\"abcd\"]\n", "", 1), "short_ids"},
		{"invalid target", validReality + "target = \"x.com\"\n", "target"},
		{"negative connection limit", validReality + "[limits]\nmax_connections = -1\n", "max_connections"},
		{"status without token", validReality + "[status]\nhttp = \"127.0.0.1:9090\"\n", "status.token"},
		{"unknown log level", "log_level = \"trace\"\n" + validReality, "log_level"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "_")+".toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseShortIDs(t *testing.T) {
	ids, err := parseShortIDs([]string{"a1b2c3d4e5f60718"})
	if err != nil {
		t.Fatal(err)
	}
	want := [8]byte{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18}
	if !ids[want] {
		t.Errorf("short id not matched: %v", ids)
	}
	ids, err = parseShortIDs([]string{"abcd"})
	if err != nil {
		t.Fatal(err)
	}
	want2 := [8]byte{0xab, 0xcd, 0, 0, 0, 0, 0, 0}
	if !ids[want2] {
		t.Errorf("padded short id not matched: %v", ids)
	}
}
