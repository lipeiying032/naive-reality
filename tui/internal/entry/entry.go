// Package entry runs the local SOCKS5/HTTP listeners and forwards into the
// client core's internal SOCKS listener, giving the TUI full traffic stats.
package entry

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"naivereal/tui/internal/stats"
)

// Manager owns the user-facing listeners.
type Manager struct {
	mu        sync.Mutex
	stats     *stats.Stats
	listeners []net.Listener
	conns     map[net.Conn]struct{}
	closed    bool
}

// NewManager creates an entry manager.
func NewManager(st *stats.Stats) *Manager {
	return &Manager{stats: st, conns: make(map[net.Conn]struct{})}
}

// Dialer returns a dial function that routes TCP connections through the
// core's internal SOCKS listener (used by the TUN data plane).
func Dialer(coreSocks string, st *stats.Stats) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		if network != "tcp" {
			return nil, fmt.Errorf("tun: unsupported network %q", network)
		}
		conn, err := socks5Dial(coreSocks, addr)
		if err != nil {
			return nil, err
		}
		// This is the upstream side of the TUN splice: writes are uploads and
		// reads are downloads.
		return stats.NewCountingConn(conn, st, false), nil
	}
}

// Start binds the SOCKS5 and HTTP listeners.
func (m *Manager) Start(socksAddr, httpAddr, coreSocks string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("entry manager closed")
	}
	dial := func(target string) (net.Conn, error) {
		return socks5Dial(coreSocks, target)
	}
	var pending []struct {
		ln     net.Listener
		handle func(net.Conn)
	}
	closePending := func() {
		for _, item := range pending {
			_ = item.ln.Close()
		}
	}
	if socksAddr != "" {
		ln, err := net.Listen("tcp", socksAddr)
		if err != nil {
			return fmt.Errorf("socks listen: %w", err)
		}
		pending = append(pending, struct {
			ln     net.Listener
			handle func(net.Conn)
		}{ln, func(c net.Conn) { serveSocks5(c, dial, m.stats) }})
	}
	if httpAddr != "" {
		ln, err := net.Listen("tcp", httpAddr)
		if err != nil {
			closePending()
			return fmt.Errorf("http listen: %w", err)
		}
		pending = append(pending, struct {
			ln     net.Listener
			handle func(net.Conn)
		}{ln, func(c net.Conn) { serveHTTPConnect(c, dial, m.stats) }})
	}
	for _, item := range pending {
		m.listeners = append(m.listeners, item.ln)
		go m.acceptLoop(item.ln, item.handle)
	}
	return nil
}

func (m *Manager) acceptLoop(ln net.Listener, handle func(net.Conn)) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			_ = c.Close()
			return
		}
		m.conns[c] = struct{}{}
		m.mu.Unlock()
		go func() {
			defer func() {
				m.mu.Lock()
				delete(m.conns, c)
				m.mu.Unlock()
			}()
			handle(c)
		}()
	}
}

// Stop closes all listeners.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, ln := range m.listeners {
		_ = ln.Close()
	}
	m.listeners = nil
	for c := range m.conns {
		_ = c.Close()
	}
	m.conns = make(map[net.Conn]struct{})
}

// socks5Dial connects to target through the core's SOCKS5 listener.
func socks5Dial(proxyAddr, target string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", proxyAddr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil { // no-auth
		c.Close()
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(c, reply); err != nil || reply[0] != 5 || reply[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("core socks handshake failed: %v", err)
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		c.Close()
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		c.Close()
		return nil, fmt.Errorf("invalid target port %q", portStr)
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 1)
			req = append(req, v4...)
		} else {
			req = append(req, 4)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			c.Close()
			return nil, fmt.Errorf("host too long")
		}
		req = append(req, 3, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port&0xff))
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	if err := readSocks5Reply(c); err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	return c, nil
}

func readSocks5Reply(r io.Reader) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fmt.Errorf("core socks connect reply: %w", err)
	}
	if header[0] != 5 {
		return fmt.Errorf("core socks connect reply has version %d", header[0])
	}
	if header[1] != 0 {
		return fmt.Errorf("core socks connect failed: rep=%d", header[1])
	}
	var addressLen int
	switch header[3] {
	case 1:
		addressLen = 4
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil {
			return fmt.Errorf("core socks domain length: %w", err)
		}
		addressLen = int(length[0])
	case 4:
		addressLen = 16
	default:
		return fmt.Errorf("core socks connect reply has address type %d", header[3])
	}
	if _, err := io.CopyN(io.Discard, r, int64(addressLen+2)); err != nil {
		return fmt.Errorf("core socks connect reply address: %w", err)
	}
	return nil
}

// serveSocks5 implements a minimal SOCKS5 CONNECT server.
func serveSocks5(c net.Conn, dial func(string) (net.Conn, error), st *stats.Stats) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil || hdr[0] != 5 {
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}
	noAuth := false
	for _, method := range methods {
		if method == 0 {
			noAuth = true
			break
		}
	}
	if !noAuth {
		_, _ = c.Write([]byte{5, 0xff})
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil { // no-auth
		return
	}
	var rhdr [4]byte
	if _, err := io.ReadFull(c, rhdr[:]); err != nil || rhdr[0] != 5 || rhdr[1] != 1 {
		return
	}
	var host string
	switch rhdr[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		return
	}
	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pb[:])
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))

	up, err := dial(target)
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	defer up.Close()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	uc := stats.NewCountingConn(c, st, true)
	splice(uc, up)
}

// serveHTTPConnect implements an HTTP CONNECT proxy entry.
func serveHTTPConnect(c net.Conn, dial func(string) (net.Conn, error), st *stats.Stats) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil || req.Method != http.MethodConnect {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	up, err := dial(req.Host)
	if err != nil {
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer up.Close()
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	uc := stats.NewCountingConn(c, st, true)
	// br may hold buffered tunnel bytes; drain them into the upstream.
	if br.Buffered() > 0 {
		peek, _ := br.Peek(br.Buffered())
		n, err := up.Write(peek)
		if n > 0 {
			st.Add(true, int64(n))
		}
		if err != nil || n != len(peek) {
			return
		}
	}
	splice(uc, up)
}

// splice copies bytes in both directions until one side closes.
func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(b, a)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(a, b)
		done <- struct{}{}
	}()
	<-done
	b.Close()
	<-done
}
