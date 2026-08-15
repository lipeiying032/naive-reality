//go:build windows

// Package sysproxy toggles the Windows system proxy (HKCU Internet Settings).
package sysproxy

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const regPath = "Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings"

// State is a snapshot of the system proxy settings.
type State struct {
	Enabled      bool
	Server       string
	Override     string
}

// Get reads the current system proxy settings.
func Get() (State, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.QUERY_VALUE)
	if err != nil {
		return State{}, err
	}
	defer k.Close()
	enabled, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil {
		return State{}, err
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil && err != registry.ErrNotExist {
		return State{}, err
	}
	override, _, err := k.GetStringValue("ProxyOverride")
	if err != nil && err != registry.ErrNotExist {
		return State{}, err
	}
	return State{Enabled: enabled != 0, Server: server, Override: override}, nil
}

// Enable points the system proxy at proxyAddr (e.g. "127.0.0.1:1080") and
// returns the previous state for later Restore.
func Enable(proxyAddr, bypass string) (State, error) {
	prev, err := Get()
	if err != nil {
		return State{}, err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.SET_VALUE)
	if err != nil {
		return State{}, err
	}
	defer k.Close()
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return State{}, err
	}
	if err := k.SetStringValue("ProxyServer", proxyAddr); err != nil {
		return State{}, err
	}
	if err := k.SetStringValue("ProxyOverride", bypass); err != nil {
		return State{}, err
	}
	refresh()
	return prev, nil
}

// Disable turns the system proxy off.
func Disable() {
	k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.SetDWordValue("ProxyEnable", 0)
	refresh()
}

// Restore returns the system proxy to a previously captured state.
func Restore(prev State) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, regPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	enabled := uint32(0)
	if prev.Enabled {
		enabled = 1
	}
	if err := k.SetDWordValue("ProxyEnable", enabled); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyServer", prev.Server); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyOverride", prev.Override); err != nil {
		return err
	}
	refresh()
	return nil
}

const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

// refresh notifies WinINET of the settings change.
func refresh() {
	wininet := syscall.NewLazyDLL("wininet.dll")
	proc := wininet.NewProc("InternetSetOptionW")
	proc.Call(0, internetOptionSettingsChanged, 0, 0)
	proc.Call(0, internetOptionRefresh, 0, 0)
}

var _ = unsafe.Pointer(nil) // keep unsafe import if needed later
var _ = fmt.Sprintf
