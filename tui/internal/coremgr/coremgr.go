// Package coremgr drives the client core (naive.exe with REALITY patches).
package coremgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"naivereal/tui/internal/config"
)

// coreConfig is the naive config.json passed to the core (official format
// plus the optional reality block).
type coreConfig struct {
	Listen              string                "json:\"listen\""
	Proxy               string                "json:\"proxy\""
	InsecureConcurrency int                   "json:\"insecure-concurrency,omitempty\""
	HostResolverRules   string                "json:\"host-resolver-rules,omitempty\""
	Reality             *config.RealityConfig "json:\"reality,omitempty\""
}

// BuildCoreConfig renders the core's config.json for a profile.
func BuildCoreConfig(p *config.Profile, internalSocks string) coreConfig {
	scheme := "https"
	if p.TLS != nil && !*p.TLS {
		scheme = "http"
	}
	proxyURL := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(p.Server, strconv.Itoa(p.Port)),
	}
	if p.Username != "" || p.Password != "" {
		proxyURL.User = url.UserPassword(p.Username, p.Password)
	}
	cc := coreConfig{
		Listen:              "socks://" + internalSocks,
		Proxy:               proxyURL.String(),
		InsecureConcurrency: p.InsecureConcurrency,
	}
	if p.Reality != nil {
		cc.Reality = p.Reality
	}
	return cc
}

// Manager runs the core process and restarts it on unexpected exits.
type Manager struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	running    bool
	generation uint64

	Logs  chan string // ring of recent core output lines (non-blocking)
	Exits chan string // unexpected exit reason (buffered, non-blocking send)
}

// NewManager creates a Manager.
func NewManager() *Manager {
	return &Manager{Logs: make(chan string, 512), Exits: make(chan string, 4)}
}

func (m *Manager) emit(line string) {
	select {
	case m.Logs <- line:
	default:
	}
}

// Start launches the core with the given profile. It restarts the core with
// backoff until ctx is cancelled or Stop is called.
func (m *Manager) Start(ctx context.Context, store *config.Store, p *config.Profile) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("core already running")
	}
	m.mu.Unlock()

	dir, err := config.Dir()
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "core-config.json")
	cc := BuildCoreConfig(p, store.InternalSocks)
	data, err := json.MarshalIndent(cc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmpPath := cfgPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("core already running")
	}
	m.generation++
	generation := m.generation
	m.running = true
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			if m.generation == generation {
				m.running = false
				m.cmd = nil
			}
			m.mu.Unlock()
		}()
		backoff := time.Second
		for {
			m.mu.Lock()
			if !m.running || m.generation != generation {
				m.mu.Unlock()
				return
			}
			// CommandContext guarantees the process dies when ctx is cancelled,
			// even if it was started between Stop() and cmd.Run().
			cmd := exec.CommandContext(ctx, store.CorePath, cfgPath)
			cmd.Stdout = &lineWriter{m: m}
			cmd.Stderr = &lineWriter{m: m}
			m.cmd = cmd
			m.mu.Unlock()

			m.emit(fmt.Sprintf("core: starting %s %s", store.CorePath, cfgPath))
			err := cmd.Run()
			m.mu.Lock()
			if m.generation == generation {
				m.cmd = nil
			}
			stopping := !m.running || m.generation != generation
			m.mu.Unlock()
			if stopping || ctx.Err() != nil {
				return
			}
			reason := fmt.Sprintf("%v", err)
			select {
			case m.Exits <- reason:
			default:
			}
			m.emit(fmt.Sprintf("core exited (%v), restart in %s", err, backoff))
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
	return nil
}

// Stop terminates the core process.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.generation++
	m.running = false
	cmd := m.cmd
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// lineWriter feeds process output into the log ring line by line.
type lineWriter struct {
	m   *Manager
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			w.m.emit(string(w.buf))
			w.buf = w.buf[:0]
			continue
		}
		w.buf = append(w.buf, b)
	}
	return len(p), nil
}
