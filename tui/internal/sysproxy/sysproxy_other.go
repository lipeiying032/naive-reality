//go:build !windows

// Package sysproxy: non-Windows stub.
package sysproxy

import "errors"

// State is a snapshot of the system proxy settings.
type State struct {
	Enabled  bool
	Server   string
	Override string
}

// Get reads the current system proxy settings.
func Get() (State, error) { return State{}, errors.New("sysproxy: not supported on this platform") }

// Enable points the system proxy at proxyAddr.
func Enable(proxyAddr, bypass string) (State, error) {
	return State{}, errors.New("sysproxy: not supported on this platform")
}

// Disable turns the system proxy off.
func Disable() {}

// Restore returns the system proxy to a previously captured state.
func Restore(prev State) error { return nil }
