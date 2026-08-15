//go:build !windows

package main

import (
	"errors"

	"naivereal/tui/internal/config"
)

// startTUN is unsupported on this platform.
func (m *model) startTUN(p *config.Profile) error {
	return errors.New("tun: only supported on windows")
}

// stopTUN is a no-op on this platform.
func (m *model) stopTUN() {}
