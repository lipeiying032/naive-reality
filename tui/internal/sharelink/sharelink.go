// Package sharelink parses and builds naive share links.
//
// Standard (v2rayN compatible, no REALITY):
//
//	naive+https://user:pass@host:port?padding=1#name
//
// Extended (this project, with REALITY):
//
//	naivereal://user:pass@host:port?server_name=X&public_key=Y&short_id=Z&fingerprint=chrome&padding=1#name
package sharelink

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"naivereal/tui/internal/config"
)

// Parse converts a share link into a Profile.
func Parse(link string) (*config.Profile, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return nil, fmt.Errorf("parse link: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "naivereal" && scheme != "naive+https" {
		return nil, fmt.Errorf("unsupported scheme %q (want naivereal:// or naive+https://)", u.Scheme)
	}
	host := u.Hostname()
	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	port := 443
	if portString := u.Port(); portString != "" {
		parsedPort, err := strconv.Atoi(portString)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return nil, fmt.Errorf("invalid port %q", portString)
		}
		port = parsedPort
	}
	if host == "" {
		return nil, fmt.Errorf("missing host")
	}
	p := &config.Profile{
		Name:                u.Fragment,
		Server:              host,
		Port:                port,
		Username:            username,
		Password:            password,
		InsecureConcurrency: 1,
		LocalSocks:          "127.0.0.1:1080",
		LocalHTTP:           "127.0.0.1:8080",
	}
	if p.Name == "" {
		p.Name = host
	}
	q := u.Query()
	if scheme == "naivereal" {
		r := &config.RealityConfig{
			ServerName:  q.Get("server_name"),
			PublicKey:   q.Get("public_key"),
			ShortID:     q.Get("short_id"),
			Fingerprint: q.Get("fingerprint"),
		}
		if r.ServerName == "" {
			return nil, fmt.Errorf("naivereal link missing server_name")
		}
		if err := validatePublicKey(r.PublicKey); err != nil {
			return nil, err
		}
		if err := validateShortID(r.ShortID); err != nil {
			return nil, err
		}
		if r.Fingerprint == "" {
			r.Fingerprint = "chrome"
		}
		p.Reality = r
	}
	return p, nil
}

// Build renders a profile as a share link (naivereal:// when reality is set).
func Build(p *config.Profile) (string, error) {
	if strings.TrimSpace(p.Server) == "" {
		return "", fmt.Errorf("missing server")
	}
	port := p.Port
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid port %d", port)
	}
	host := p.Server
	if p.Port != 0 && p.Port != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	} else if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	u := &url.URL{Host: host, Fragment: p.Name}
	if p.Username != "" || p.Password != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	q := url.Values{}
	if p.Reality != nil {
		if strings.TrimSpace(p.Reality.ServerName) == "" {
			return "", fmt.Errorf("missing reality server_name")
		}
		if err := validatePublicKey(p.Reality.PublicKey); err != nil {
			return "", err
		}
		if err := validateShortID(p.Reality.ShortID); err != nil {
			return "", err
		}
		u.Scheme = "naivereal"
		q.Set("server_name", p.Reality.ServerName)
		q.Set("public_key", p.Reality.PublicKey)
		q.Set("short_id", p.Reality.ShortID)
		if p.Reality.Fingerprint != "" && p.Reality.Fingerprint != "chrome" {
			q.Set("fingerprint", p.Reality.Fingerprint)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	u.Scheme = "naive+https"
	return u.String(), nil
}

func validatePublicKey(s string) error {
	if s == "" {
		return fmt.Errorf("missing public_key")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("public_key must be base64url of 32 bytes")
	}
	return nil
}

func validateShortID(s string) error {
	if s == "" {
		return nil // zero short id is legal
	}
	if len(s) > 16 {
		return fmt.Errorf("short_id too long (max 16 hex chars)")
	}
	if _, err := hex.DecodeString(s); err != nil {
		return fmt.Errorf("short_id must be hex")
	}
	return nil
}
