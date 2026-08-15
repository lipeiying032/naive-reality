//go:build !windows

// Package tun: TUN mode is only supported on Windows in this build.
package tun

import (
	"errors"
	"net"
)

// Device is a TUN device (stub).
type Device struct{}

// Create is unsupported on this platform.
func Create(cfg Config) (*Device, error) {
	return nil, errors.New("tun: only supported on windows")
}

// Close closes the device (stub).
func (d *Device) Close() error { return nil }

// Config holds TUN settings.
type Config struct {
	Gateway   string   // virtual gateway IP on the TUN subnet, e.g. 198.18.0.1
	Subnet    string   // TUN subnet CIDR, e.g. 198.18.0.0/15
	MTU       int      // interface MTU (default 1500)
	DoH       []string // DoH endpoints for fake-DNS
	ExcludeIP []string // server IPs routed via the physical gateway
	Dial      func(network, addr string) (net.Conn, error)
}
