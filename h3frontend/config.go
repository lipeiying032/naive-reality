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

const (
	defaultStreamReceiveWindow = uint64(8 * 1024 * 1024)
	defaultConnReceiveWindow   = defaultStreamReceiveWindow * 5 / 2
)

type Config struct {
	LogLevel   string           `toml:"log_level"`
	Listen     string           `toml:"listen"`
	Mode       string           `toml:"mode"` // "tls" (default) | "reality"
	TLS        TLSConfig        `toml:"tls"`
	Reality    RealityConfig    `toml:"reality"`
	QUIC       QUICConfig       `toml:"quic"`
	Congestion CongestionConfig `toml:"congestion"`
	Upstream   UpstreamConfig   `toml:"upstream"`
}

// RealityConfig mirrors the frontend's inbound.reality block for the QUIC
// REALITY-over-QUIC (C-gamma) mode.
type RealityConfig struct {
	PrivateKey      string   `toml:"private_key"`
	ShortIDs        []string `toml:"short_ids"`
	ServerNames     []string `toml:"server_names"`
	Dest            string   `toml:"dest"`
	DestServerName  string   `toml:"dest_server_name"`
	H3Cert          string   `toml:"h3_cert"`
	H3Key           string   `toml:"h3_key"`
	MaxTimeDiff     int64    `toml:"max_time_diff"` // milliseconds; 0 = disabled
	FallbackTimeout string   `toml:"fallback_timeout"`
}

type TLSConfig struct {
	Cert string `toml:"cert"`
	Key  string `toml:"key"`
}

type QUICConfig struct {
	InitialPacketSize              uint16 `toml:"initPacketSize"`
	InitialStreamReceiveWindow     uint64 `toml:"initStreamReceiveWindow"`
	MaxStreamReceiveWindow         uint64 `toml:"maxStreamReceiveWindow"`
	InitialConnectionReceiveWindow uint64 `toml:"initConnReceiveWindow"`
	MaxConnectionReceiveWindow     uint64 `toml:"maxConnReceiveWindow"`
	MaxIdleTimeout                 string `toml:"maxIdleTimeout"`
	MaxIncomingStreams             int64  `toml:"maxIncomingStreams"`
	DisablePathMTUDiscovery        bool   `toml:"disablePathMTUDiscovery"`
	DisableGSO                     bool   `toml:"disableGSO"`
	DisablePathManager             bool   `toml:"disablePathManager"`
}

type CongestionConfig struct {
	Type       string `toml:"type"`
	BBRProfile string `toml:"bbrProfile"`
}

type UpstreamConfig struct {
	Addr string `toml:"addr"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validateAndFill(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validateAndFill() error {
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level %q: must be debug, info, warn, or error", c.LogLevel)
	}
	if c.Listen == "" {
		c.Listen = "0.0.0.0:443"
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	switch c.Mode {
	case "":
		c.Mode = "tls"
	case "tls", "reality":
	default:
		return fmt.Errorf("mode %q: must be \"tls\" or \"reality\"", c.Mode)
	}
	switch c.Mode {
	case "tls":
		if c.TLS.Cert == "" || c.TLS.Key == "" {
			return fmt.Errorf("tls.cert and tls.key are required in tls mode")
		}
		if _, err := os.Stat(c.TLS.Cert); err != nil {
			return fmt.Errorf("tls.cert: %w", err)
		}
		if _, err := os.Stat(c.TLS.Key); err != nil {
			return fmt.Errorf("tls.key: %w", err)
		}
	case "reality":
		if c.Reality.PrivateKey == "" {
			return fmt.Errorf("reality.private_key is required in reality mode")
		}
		if _, err := parseRealityPrivateKey(c.Reality.PrivateKey); err != nil {
			return err
		}
		if len(c.Reality.ShortIDs) == 0 {
			return fmt.Errorf("reality.short_ids must contain at least one ID")
		}
		if _, err := parseShortIDs(c.Reality.ShortIDs); err != nil {
			return err
		}
		if len(c.Reality.ServerNames) == 0 {
			return fmt.Errorf("reality.server_names must contain at least one SNI")
		}
		for i, name := range c.Reality.ServerNames {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("reality.server_names[%d] must not be empty", i)
			}
			c.Reality.ServerNames[i] = name
		}
		if c.Reality.Dest == "" {
			return fmt.Errorf("reality.dest is required in reality mode")
		}
		if _, _, err := net.SplitHostPort(c.Reality.Dest); err != nil {
			return fmt.Errorf("reality.dest %q: %w", c.Reality.Dest, err)
		}
		if c.Reality.DestServerName == "" {
			c.Reality.DestServerName = c.Reality.ServerNames[0]
		}
		if c.Reality.MaxTimeDiff < 0 {
			return fmt.Errorf("reality.max_time_diff must not be negative")
		}
		if c.Reality.FallbackTimeout == "" {
			c.Reality.FallbackTimeout = "120s"
		}
		if _, err := time.ParseDuration(c.Reality.FallbackTimeout); err != nil {
			return fmt.Errorf("reality.fallback_timeout: %w", err)
		}
		if (c.Reality.H3Cert == "") != (c.Reality.H3Key == "") {
			return fmt.Errorf("reality.h3_cert and reality.h3_key must be set together")
		}
		if c.Reality.H3Cert != "" {
			if _, err := os.Stat(c.Reality.H3Cert); err != nil {
				return fmt.Errorf("reality.h3_cert: %w", err)
			}
			if _, err := os.Stat(c.Reality.H3Key); err != nil {
				return fmt.Errorf("reality.h3_key: %w", err)
			}
		}
	}
	if c.Upstream.Addr == "" {
		c.Upstream.Addr = "127.0.0.1:18080"
	}
	if _, _, err := net.SplitHostPort(c.Upstream.Addr); err != nil {
		return fmt.Errorf("upstream.addr %q: %w", c.Upstream.Addr, err)
	}

	if c.QUIC.InitialPacketSize == 0 {
		c.QUIC.InitialPacketSize = 1200
	} else if c.QUIC.InitialPacketSize < 1200 {
		return fmt.Errorf("quic.initPacketSize must be at least 1200")
	}

	setWindow := func(name string, v *uint64, def uint64) error {
		if *v == 0 {
			*v = def
		} else if *v < 16384 {
			return fmt.Errorf("quic.%s must be at least 16384", name)
		}
		return nil
	}
	if err := setWindow("initStreamReceiveWindow", &c.QUIC.InitialStreamReceiveWindow, defaultStreamReceiveWindow); err != nil {
		return err
	}
	if err := setWindow("maxStreamReceiveWindow", &c.QUIC.MaxStreamReceiveWindow, defaultStreamReceiveWindow); err != nil {
		return err
	}
	if err := setWindow("initConnReceiveWindow", &c.QUIC.InitialConnectionReceiveWindow, defaultConnReceiveWindow); err != nil {
		return err
	}
	if err := setWindow("maxConnReceiveWindow", &c.QUIC.MaxConnectionReceiveWindow, defaultConnReceiveWindow); err != nil {
		return err
	}
	if c.QUIC.MaxIdleTimeout == "" {
		c.QUIC.MaxIdleTimeout = "30s"
	}
	if _, err := time.ParseDuration(c.QUIC.MaxIdleTimeout); err != nil {
		return fmt.Errorf("quic.maxIdleTimeout: %w", err)
	}
	if c.QUIC.MaxIncomingStreams == 0 {
		c.QUIC.MaxIncomingStreams = 1024
	}
	if c.QUIC.MaxIncomingStreams < 8 {
		return fmt.Errorf("quic.maxIncomingStreams must be at least 8")
	}
	if c.Congestion.Type == "" {
		c.Congestion.Type = "bbr"
	}
	switch c.Congestion.Type {
	case "bbr", "cubic":
	default:
		return fmt.Errorf("congestion.type %q: must be bbr or cubic", c.Congestion.Type)
	}
	if c.Congestion.BBRProfile == "" {
		c.Congestion.BBRProfile = "standard"
	}
	switch c.Congestion.BBRProfile {
	case "standard", "aggressive", "conservative":
	default:
		return fmt.Errorf("congestion.bbrProfile %q: must be standard, aggressive, or conservative", c.Congestion.BBRProfile)
	}
	return nil
}

// parseRealityPrivateKey decodes the base64url-encoded 32-byte X25519 private key.
func parseRealityPrivateKey(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("reality.private_key: expect base64url: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("reality.private_key: got %d bytes, want 32", len(b))
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
				return nil, fmt.Errorf("reality.short_ids: %q must be hex with at most 16 characters", s)
			}
			copy(id[:], b) // left aligned, right zero padded (matches Xray)
		}
		m[id] = true
	}
	return m, nil
}
