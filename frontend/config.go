package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config mirrors frontend.toml.
type Config struct {
	LogLevel string         "toml:\"log_level\""
	Inbound  InboundConfig  "toml:\"inbound\""
	Upstream UpstreamConfig "toml:\"upstream\""
	Limits   LimitsConfig   "toml:\"limits\""
	Status   StatusConfig   "toml:\"status\""
}

type InboundConfig struct {
	Listen  string        "toml:\"listen\""
	Mode    string        "toml:\"mode\"" // "reality" | "tls"
	Reality RealityConfig "toml:\"reality\""
	TLS     TLSConfig     "toml:\"tls\""
}

type RealityConfig struct {
	PrivateKey   string   "toml:\"private_key\""
	ShortIDs     []string "toml:\"short_ids\""
	ServerNames  []string "toml:\"server_names\""
	Target       string   "toml:\"target\""
	RelayEnabled *bool    "toml:\"relay_enabled\""
	MaxTimeDiff  int64    "toml:\"max_time_diff\"" // milliseconds; 0 = disabled
}

type TLSConfig struct {
	Cert string "toml:\"cert\""
	Key  string "toml:\"key\""
}

type UpstreamConfig struct {
	Addr string "toml:\"addr\""
}

type LimitsConfig struct {
	MaxConnections   int      "toml:\"max_connections\""
	MaxRelays        int      "toml:\"max_relays\""
	HandshakeTimeout Duration "toml:\"handshake_timeout\""
	IdleTimeout      Duration "toml:\"idle_timeout\""
}

type StatusConfig struct {
	HTTP  string "toml:\"http\""
	Token string "toml:\"token\""
}

// Duration is a time.Duration that unmarshals from a TOML string like "10s".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(text), err)
	}
	d.Duration = v
	return nil
}

// LoadConfig reads, parses and validates the frontend configuration.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaultsAndValidate() error {
	if c.Inbound.Listen == "" {
		c.Inbound.Listen = "0.0.0.0:443"
	}
	if _, _, err := net.SplitHostPort(c.Inbound.Listen); err != nil {
		return fmt.Errorf("inbound.listen %q: %w", c.Inbound.Listen, err)
	}
	if c.Upstream.Addr == "" {
		c.Upstream.Addr = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(c.Upstream.Addr); err != nil {
		return fmt.Errorf("upstream.addr %q: %w", c.Upstream.Addr, err)
	}
	switch c.Inbound.Mode {
	case "":
		c.Inbound.Mode = "reality"
	case "reality", "tls":
	default:
		return fmt.Errorf("inbound.mode %q: must be \"reality\" or \"tls\"", c.Inbound.Mode)
	}
	switch c.Inbound.Mode {
	case "reality":
		if c.Inbound.Reality.PrivateKey == "" {
			return fmt.Errorf("inbound.reality.private_key is required in reality mode")
		}
		if len(c.Inbound.Reality.ServerNames) == 0 {
			return fmt.Errorf("inbound.reality.server_names must contain at least one SNI")
		}
		if _, err := parseRealityPrivateKey(c.Inbound.Reality.PrivateKey); err != nil {
			return err
		}
		if _, err := parseShortIDs(c.Inbound.Reality.ShortIDs); err != nil {
			return err
		}
		if c.Inbound.Reality.Target == "" {
			c.Inbound.Reality.Target = c.Inbound.Reality.ServerNames[0] + ":443"
		}
	case "tls":
		if c.Inbound.TLS.Cert == "" || c.Inbound.TLS.Key == "" {
			return fmt.Errorf("inbound.tls.cert and inbound.tls.key are required in tls mode")
		}
		if _, err := os.Stat(c.Inbound.TLS.Cert); err != nil {
			return fmt.Errorf("inbound.tls.cert: %w", err)
		}
		if _, err := os.Stat(c.Inbound.TLS.Key); err != nil {
			return fmt.Errorf("inbound.tls.key: %w", err)
		}
	}
	if c.Limits.MaxConnections <= 0 {
		c.Limits.MaxConnections = 1024
	}
	if c.Limits.MaxRelays <= 0 {
		c.Limits.MaxRelays = 64
	}
	if c.Limits.HandshakeTimeout.Duration <= 0 {
		c.Limits.HandshakeTimeout.Duration = 10 * time.Second
	}
	if c.Limits.IdleTimeout.Duration <= 0 {
		c.Limits.IdleTimeout.Duration = 300 * time.Second
	}
	return nil
}

// parseRealityPrivateKey decodes the base64url-encoded 32-byte X25519 private key.
func parseRealityPrivateKey(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("inbound.reality.private_key: expect base64url: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("inbound.reality.private_key: got %d bytes, want 32", len(b))
	}
	return b, nil
}

// parseShortIDs converts a list of hex short IDs (<=16 hex chars each, "" allowed)
// into the fixed 8-byte, right-zero-padded set used by REALITY.
func parseShortIDs(list []string) (map[[8]byte]bool, error) {
	m := make(map[[8]byte]bool, len(list))
	for _, s := range list {
		var id [8]byte
		if s != "" {
			b, err := hex.DecodeString(strings.TrimSpace(s))
			if err != nil || len(b) > 8 {
				return nil, fmt.Errorf("inbound.reality.short_ids: %q must be hex with at most 16 characters", s)
			}
			copy(id[:], b) // left aligned, right zero padded (matches Xray)
		}
		m[id] = true
	}
	return m, nil
}
