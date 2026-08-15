package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/atotto/clipboard"

	"naivereal/tui/internal/config"
	"naivereal/tui/internal/coremgr"
	"naivereal/tui/internal/entry"
	"naivereal/tui/internal/sharelink"
	"naivereal/tui/internal/stats"
	"naivereal/tui/internal/sysproxy"
)

type model struct {
	store  *config.Store
	core   *coremgr.Manager
	entry  *entry.Manager
	tun    tunDevice
	stats  *stats.Stats
	cancel context.CancelFunc
	ctx    context.Context

	state string // disconnected | connecting | connected | error
	err   string
	tab   int // 0 status, 1 profiles, 2 logs
	logs  []string

	// profile tab
	cursor  int
	editing bool
	input   textinput.Model
	prevSP  sysproxy.State
	spOn    bool
	width   int
	height  int
	msg     string // transient status line
}

// tunDevice abstracts the TUN data plane (implemented on windows).
type tunDevice interface {
	Close() error
}

type tickMsg time.Time

type coreLogMsg string

func newModel() model {
	ti := textinput.New()
	ti.Placeholder = "paste share link (naive+https:// or naivereal://)"
	ti.Width = 70
	m := model{
		store: &config.Store{},
		core:  coremgr.NewManager(),
		stats: &stats.Stats{},
		state: "disconnected",
		input: ti,
	}
	store, err := config.Load()
	if err != nil {
		m.state = "error"
		m.err = fmt.Sprintf("load config: %v", err)
		return m
	}
	m.store = store
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.tick(), m.waitLog(), m.waitExit())
}

func (m model) tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) waitLog() tea.Cmd {
	return func() tea.Msg {
		return coreLogMsg(<-m.core.Logs)
	}
}

type coreExitMsg string

func (m model) waitExit() tea.Cmd {
	return func() tea.Msg {
		return coreExitMsg(<-m.core.Exits)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if m.editing {
			switch msg.String() {
			case "enter":
				m.importLink()
				m.editing = false
				m.input.Reset()
				return m, nil
			case "esc":
				m.editing = false
				m.input.Reset()
				return m, nil
			default:
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.disconnect()
			return m, tea.Quit
		case "tab":
			m.tab = (m.tab + 1) % 3
		case "c":
			m.connect()
		case "d":
			m.disconnect()
		case "a":
			if m.tab == 1 {
				m.editing = true
				m.input.Focus()
				return m, nil
			}
		case "x":
			m.copyLink()
		case "up", "k":
			if m.tab == 1 && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.tab == 1 && m.cursor < len(m.store.Profiles)-1 {
				m.cursor++
			}
		case "enter":
			if m.tab == 1 && len(m.store.Profiles) > 0 {
				m.store.ActiveProfile = m.store.Profiles[m.cursor].Name
				if err := m.store.Save(); err != nil {
					m.msg = "save: " + err.Error()
				} else {
					m.msg = "activated: " + m.store.ActiveProfile
				}
			}
		}
	case tickMsg:
		return m, m.tick()
	case coreExitMsg:
		if msg != "" {
			if m.state == "connected" || m.state == "connecting" {
				m.state = "reconnecting"
			}
			m.logs = append(m.logs, "core exited: "+string(msg)+" (restarting)")
			if len(m.logs) > 200 {
				m.logs = m.logs[len(m.logs)-200:]
			}
		}
		return m, m.waitExit()
	case coreLogMsg:
		if msg != "" {
			m.logs = append(m.logs, string(msg))
			if len(m.logs) > 200 {
				m.logs = m.logs[len(m.logs)-200:]
			}
		}
		return m, m.waitLog()
	}
	return m, nil
}

func (m *model) importLink() {
	link := strings.TrimSpace(m.input.Value())
	if link == "" {
		return
	}
	p, err := sharelink.Parse(link)
	if err != nil {
		m.msg = "import: " + err.Error()
		return
	}
	m.store.Profiles = append(m.store.Profiles, *p)
	if m.store.ActiveProfile == "" {
		m.store.ActiveProfile = p.Name
	}
	if err := m.store.Save(); err != nil {
		m.msg = "save: " + err.Error()
		return
	}
	m.msg = "imported: " + p.Name
}

func (m *model) copyLink() {
	p := m.store.Active()
	if p == nil {
		m.msg = "no profile"
		return
	}
	link, err := sharelink.Build(p)
	if err != nil {
		m.msg = "build link: " + err.Error()
		return
	}
	if err := clipboard.WriteAll(link); err != nil {
		m.msg = "clipboard: " + err.Error()
		return
	}
	m.msg = "share link copied"
}

func (m *model) connect() {
	if m.state == "connected" || m.state == "connecting" {
		return
	}
	p := m.store.Active()
	if p == nil {
		m.err = "no profile configured"
		m.state = "error"
		return
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.entry = entry.NewManager(m.stats)
	m.state = "connecting"
	m.err = ""
	m.msg = ""
	if err := m.core.Start(m.ctx, m.store, p); err != nil {
		m.err = err.Error()
		m.state = "error"
		return
	}
	if err := m.entry.Start(p.LocalSocks, p.LocalHTTP, m.store.InternalSocks); err != nil {
		m.err = err.Error()
		m.state = "error"
		m.core.Stop()
		return
	}
	if p.TUN != nil && p.TUN.Enabled {
		if err := m.startTUN(p); err != nil {
			m.logs = append(m.logs, "tun: "+err.Error())
		}
	}
	if p.SystemProxy != nil && p.SystemProxy.Enabled {
		prev, err := sysproxy.Enable(p.LocalSocks, p.SystemProxy.Bypass)
		if err != nil {
			m.logs = append(m.logs, "sysproxy: "+err.Error())
		} else {
			m.prevSP, m.spOn = prev, true
		}
	}
	m.state = "connected"
	m.logs = append(m.logs, fmt.Sprintf("connected: %s (socks %s, http %s)", p.Name, p.LocalSocks, p.LocalHTTP))
}

func (m *model) disconnect() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.core != nil {
		m.core.Stop()
	}
	if m.entry != nil {
		m.entry.Stop()
		m.entry = nil
	}
	m.stopTUN()
	if m.spOn {
		sysproxy.Restore(m.prevSP)
		m.spOn = false
	}
	if m.state != "disconnected" && m.state != "" {
		m.state = "disconnected"
	}
}

func (m model) View() string {
	var b strings.Builder
	p := m.store.Active()
	name := "(none)"
	if p != nil {
		name = p.Name
	}
	b.WriteString(fmt.Sprintf("naivereal TUI  profile: %s  state: %s\n\n", name, m.state))
	switch m.tab {
	case 0:
		upR, downR := m.stats.Rates()
		b.WriteString(fmt.Sprintf("up:   %s (%s/s)\n", humanBytes(m.stats.UpBytes.Load()), humanBytes(int64(upR))))
		b.WriteString(fmt.Sprintf("down: %s (%s/s)\n", humanBytes(m.stats.DownBytes.Load()), humanBytes(int64(downR))))
		if p != nil {
			b.WriteString(fmt.Sprintf("server: %s:%d  username: %s  reality: %v  sysproxy: %v\n", p.Server, p.Port, p.Username, p.Reality != nil, p.SystemProxy != nil && p.SystemProxy.Enabled))
			b.WriteString(fmt.Sprintf("socks: %s  http: %s\n", p.LocalSocks, p.LocalHTTP))
		}
		if m.err != "" {
			b.WriteString("error: " + m.err + "\n")
		}
	case 1:
		b.WriteString("--- profiles (enter=activate, a=import link, x=copy link) ---\n")
		for i, pr := range m.store.Profiles {
			mark := " "
			if pr.Name == m.store.ActiveProfile {
				mark = "*"
			}
			if i == m.cursor {
				mark = ">"
			}
			reality := ""
			if pr.Reality != nil {
				reality = " [reality]"
			}
			b.WriteString(fmt.Sprintf("%s %s  %s:%d%s\n", mark, pr.Name, pr.Server, pr.Port, reality))
		}
		if len(m.store.Profiles) == 0 {
			b.WriteString("(empty - press a and paste a share link)\n")
		}
	case 2:
		b.WriteString("--- logs ---\n")
		start := 0
		if len(m.logs) > 30 {
			start = len(m.logs) - 30
		}
		for _, l := range m.logs[start:] {
			b.WriteString(l + "\n")
		}
	}
	if m.editing {
		b.WriteString("\n" + m.input.View() + "\n")
	}
	if m.msg != "" {
		b.WriteString("msg: " + m.msg + "\n")
	}
	b.WriteString("\n[c] connect  [d] disconnect  [tab] switch  [q] quit\n")
	return b.String()
}

func humanBytes(n int64) string {
	f := float64(n)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%.2f GB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%.2f MB", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%.2f KB", f/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
