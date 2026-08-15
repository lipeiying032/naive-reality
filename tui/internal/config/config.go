// Package config holds the TUI client's profile store.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// RealityConfig mirrors the core's reality block.
type RealityConfig struct {
	ServerName  string "json:\"server_name\""
	PublicKey   string "json:\"public_key\""
	ShortID     string "json:\"short_id\""
	Fingerprint string "json:\"fingerprint,omitempty\""
}

// SysProxyConfig controls the Windows system proxy toggle.
type SysProxyConfig struct {
	Enabled bool   "json:\"enabled\""
	Bypass  string "json:\"bypass,omitempty\""
}

// Profile is one proxy node.
type Profile struct {
	Name                string          "json:\"name\""
	Server              string          "json:\"server\""
	Port                int             "json:\"port\""
	Username            string          "json:\"username\""
	Password            string          "json:\"password\""
	InsecureConcurrency int             "json:\"insecure_concurrency,omitempty\""
	Reality             *RealityConfig  "json:\"reality,omitempty\""
	TLS                 *bool           "json:\"tls,omitempty\"" // nil = true; false = plain h1 hop (testing/LAN)
	LocalSocks          string          "json:\"local_socks\""
	LocalHTTP           string          "json:\"local_http\""
	SystemProxy         *SysProxyConfig "json:\"system_proxy,omitempty\""
	TUN                 *TUNConfig      "json:\"tun,omitempty\""
}

// TUNConfig controls TUN mode for a profile.
type TUNConfig struct {
	Enabled   bool     "json:\"enabled\""
	Gateway   string   "json:\"gateway,omitempty\"" // e.g. 198.18.0.1
	Subnet    string   "json:\"subnet,omitempty\""  // e.g. 198.18.0.0/15
	MTU       int      "json:\"mtu,omitempty\""
	DoH       []string "json:\"doh,omitempty\""        // DoH endpoints for fake-DNS
	ExcludeIP []string "json:\"exclude_ip,omitempty\"" // server IPs via physical gateway
}

// Store is the persisted application state.
type Store struct {
	ActiveProfile string    "json:\"active_profile\""
	Profiles      []Profile "json:\"profiles\""
	CorePath      string    "json:\"core_path,omitempty\""      // naive.exe location
	InternalSocks string    "json:\"internal_socks,omitempty\"" // core's local socks entry
	LogLevel      string    "json:\"log_level,omitempty\""
}

// Dir returns the per-user config directory (%APPDATA%/naivereal on Windows).
func Dir() (string, error) {
	var dir string
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, "AppData", "Roaming")
		}
		dir = filepath.Join(base, "naivereal")
	default:
		cfg, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cfg, "naivereal")
	}
	return dir, nil
}

// Path returns the store file path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "profiles.json"), nil
}

// Load reads the store, applying defaults.
func Load() (*Store, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	s := &Store{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.applyDefaults()
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.applyDefaults()
	return s, nil
}

func (s *Store) applyDefaults() {
	if s.InternalSocks == "" {
		s.InternalSocks = "127.0.0.1:41080"
	}
	if s.CorePath == "" {
		s.CorePath = "naive.exe"
	}
	if s.LogLevel == "" {
		s.LogLevel = "info"
	}
	for i := range s.Profiles {
		if s.Profiles[i].Port == 0 {
			s.Profiles[i].Port = 443
		}
		if s.Profiles[i].LocalSocks == "" {
			s.Profiles[i].LocalSocks = "127.0.0.1:1080"
		}
		if s.Profiles[i].LocalHTTP == "" {
			s.Profiles[i].LocalHTTP = "127.0.0.1:8080"
		}
		if s.Profiles[i].InsecureConcurrency <= 0 {
			s.Profiles[i].InsecureConcurrency = 1
		}
		if s.Profiles[i].Reality != nil && s.Profiles[i].Reality.Fingerprint == "" {
			s.Profiles[i].Reality.Fingerprint = "chrome"
		}
		if s.Profiles[i].TUN != nil && s.Profiles[i].TUN.Gateway == "" {
			s.Profiles[i].TUN.Gateway = "198.18.0.1"
		}
		if s.Profiles[i].TUN != nil && s.Profiles[i].TUN.Subnet == "" {
			s.Profiles[i].TUN.Subnet = "198.18.0.0/15"
		}
		if s.Profiles[i].TUN != nil && s.Profiles[i].TUN.MTU == 0 {
			s.Profiles[i].TUN.MTU = 1500
		}
	}
}

// Save atomically persists the store.
func (s *Store) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Active returns the currently selected profile, or nil.
func (s *Store) Active() *Profile {
	for i := range s.Profiles {
		if s.Profiles[i].Name == s.ActiveProfile {
			return &s.Profiles[i]
		}
	}
	if len(s.Profiles) > 0 {
		return &s.Profiles[0]
	}
	return nil
}
