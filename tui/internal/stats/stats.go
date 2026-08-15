// Package stats provides byte counters with rate estimation.
package stats

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Stats tracks total bytes and a smoothed byte rate.
type Stats struct {
	UpBytes   atomic.Int64
	DownBytes atomic.Int64

	mu      sync.Mutex
	upRate  float64
	downRate float64
	last    time.Time
	lastUp  int64
	lastDown int64
}

// Add records n bytes in the given direction.
func (s *Stats) Add(up bool, n int64) {
	if up {
		s.UpBytes.Add(n)
	} else {
		s.DownBytes.Add(n)
	}
	s.mu.Lock()
	now := time.Now()
	if !s.last.IsZero() && now.After(s.last) {
		dt := now.Sub(s.last).Seconds()
		if dt > 0.5 {
			s.upRate = s.upRate*0.7 + float64(s.UpBytes.Load()-s.lastUp)/dt*0.3
			s.downRate = s.downRate*0.7 + float64(s.DownBytes.Load()-s.lastDown)/dt*0.3
			s.last = now
			s.lastUp = s.UpBytes.Load()
			s.lastDown = s.DownBytes.Load()
		}
	} else if s.last.IsZero() {
		s.last = now
		s.lastUp = s.UpBytes.Load()
		s.lastDown = s.DownBytes.Load()
	}
	s.mu.Unlock()
}

// Rates returns the smoothed up/down rates in bytes per second.
func (s *Stats) Rates() (up, down float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upRate, s.downRate
}

// CountingConn wraps a net.Conn and counts bytes in one direction.
type CountingConn struct {
	net.Conn
	stats   *Stats
	countUp bool
}

// NewCountingConn wraps c so that Read counts as up and Write as down
// (when countUp is true), or vice versa.
func NewCountingConn(c net.Conn, st *Stats, countUp bool) net.Conn {
	return &CountingConn{Conn: c, stats: st, countUp: countUp}
}

func (c *CountingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.stats.Add(c.countUp, int64(n))
	}
	return n, err
}

func (c *CountingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.stats.Add(!c.countUp, int64(n))
	}
	return n, err
}


