package main

import (
	"fmt"
	"net"
	"os"
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
	TLS        TLSConfig        `toml:"tls"`
	QUIC       QUICConfig       `toml:"quic"`
	Congestion CongestionConfig `toml:"congestion"`
	Upstream   UpstreamConfig   `toml:"upstream"`
}

type TLSConfig struct {
	Cert string `toml:"cert"`
	Key  string `toml:"key"`
}

type QUICConfig struct {
	InitialStreamReceiveWindow     uint64 `toml:"initStreamReceiveWindow"`
	MaxStreamReceiveWindow         uint64 `toml:"maxStreamReceiveWindow"`
	InitialConnectionReceiveWindow uint64 `toml:"initConnReceiveWindow"`
	MaxConnectionReceiveWindow     uint64 `toml:"maxConnReceiveWindow"`
	MaxIdleTimeout                 string `toml:"maxIdleTimeout"`
	MaxIncomingStreams             int64  `toml:"maxIncomingStreams"`
	DisablePathMTUDiscovery        bool   `toml:"disablePathMTUDiscovery"`
	DisableGSO                     bool   `toml:"disableGSO"`
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
	if c.TLS.Cert == "" || c.TLS.Key == "" {
		return fmt.Errorf("tls.cert and tls.key are required")
	}
	if _, err := os.Stat(c.TLS.Cert); err != nil {
		return fmt.Errorf("tls.cert: %w", err)
	}
	if _, err := os.Stat(c.TLS.Key); err != nil {
		return fmt.Errorf("tls.key: %w", err)
	}
	if c.Upstream.Addr == "" {
		c.Upstream.Addr = "127.0.0.1:18080"
	}
	if _, _, err := net.SplitHostPort(c.Upstream.Addr); err != nil {
		return fmt.Errorf("upstream.addr %q: %w", c.Upstream.Addr, err)
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
	if c.Congestion.Type != "bbr" {
		return fmt.Errorf("congestion.type %q: only bbr is supported by the h3 frontend", c.Congestion.Type)
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
