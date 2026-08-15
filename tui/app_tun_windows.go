//go:build windows

package main

import (
	"fmt"
	"net"

	"naivereal/tui/internal/config"
	"naivereal/tui/internal/entry"
	"naivereal/tui/internal/tun"
)

// startTUN brings up the TUN data plane for the profile.
func (m *model) startTUN(p *config.Profile) error {
	tc := p.TUN
	exclude := append([]string(nil), tc.ExcludeIP...)
	serverIPs, err := resolveServerIPv4(p.Server)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(exclude)+len(serverIPs))
	for _, ip := range exclude {
		seen[ip] = true
	}
	for _, ip := range serverIPs {
		if !seen[ip] {
			exclude = append(exclude, ip)
			seen[ip] = true
		}
	}
	d, err := tun.Create(tun.Config{
		Gateway:   tc.Gateway,
		Subnet:    tc.Subnet,
		MTU:       tc.MTU,
		DoH:       tc.DoH,
		ExcludeIP: exclude,
		Dial:      entry.Dialer(m.store.InternalSocks, m.stats),
	})
	if err != nil {
		return err
	}
	m.tun = d
	m.logs = append(m.logs, "tun: up (gateway "+tc.Gateway+")")
	return nil
}

func resolveServerIPv4(server string) ([]string, error) {
	if ip := net.ParseIP(server); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return []string{v4.String()}, nil
		}
		return nil, nil
	}
	ips, err := net.LookupIP(server)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy server %q before enabling TUN: %w", server, err)
	}
	var out []string
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("proxy server %q has no IPv4 address to exclude from TUN routes", server)
	}
	return out, nil
}

// stopTUN tears the TUN data plane down.
func (m *model) stopTUN() {
	if m.tun != nil {
		m.tun.Close()
		m.tun = nil
		m.logs = append(m.logs, "tun: down")
	}
}
