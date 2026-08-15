//go:build windows

package main

import (
	"naivereal/tui/internal/config"
	"naivereal/tui/internal/entry"
	"naivereal/tui/internal/tun"
)

// startTUN brings up the TUN data plane for the profile.
func (m *model) startTUN(p *config.Profile) error {
	tc := p.TUN
	d, err := tun.Create(tun.Config{
		Gateway:   tc.Gateway,
		Subnet:    tc.Subnet,
		MTU:       tc.MTU,
		DoH:       tc.DoH,
		ExcludeIP: tc.ExcludeIP,
		Dial:      entry.Dialer(m.store.InternalSocks),
	})
	if err != nil {
		return err
	}
	m.tun = d
	m.logs = append(m.logs, "tun: up (gateway "+tc.Gateway+")")
	return nil
}

// stopTUN tears the TUN data plane down.
func (m *model) stopTUN() {
	if m.tun != nil {
		m.tun.Close()
		m.tun = nil
		m.logs = append(m.logs, "tun: down")
	}
}
