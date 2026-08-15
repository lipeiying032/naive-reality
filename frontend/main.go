package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	reality "github.com/xtls/reality"
)

const version = "0.1.0"

var log = slog.New(slog.NewTextHandler(os.Stderr, nil))

// tunnelServer terminates REALITY (or plain TLS) and relays CONNECT streams
// to the official naive server over HTTP/1.1.
type tunnelServer struct {
	cfg        *Config
	rCfg       *reality.Config
	tlsCfg     *tls.Config
	stats      *Stats
	relaySlots chan struct{}
}

type relayConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *relayConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// NewTunnelServer builds the server from a validated configuration.
func NewTunnelServer(cfg *Config) (*tunnelServer, error) {
	s := &tunnelServer{cfg: cfg, stats: &Stats{}}

	switch cfg.Inbound.Mode {
	case "reality":
		relay := cfg.Inbound.Reality.RelayEnabled == nil || *cfg.Inbound.Reality.RelayEnabled
		s.relaySlots = make(chan struct{}, cfg.Limits.MaxRelays)
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		dialContext := func(ctx context.Context, network, address string) (net.Conn, error) {
			if !relay {
				return nil, fmt.Errorf("relay disabled")
			}
			select {
			case s.relaySlots <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				return nil, fmt.Errorf("relay limit reached")
			}
			release := func() { <-s.relaySlots }
			if address == "" {
				address = cfg.Inbound.Reality.Target
			}
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				release()
				return nil, err
			}
			return &relayConn{Conn: conn, release: release}, nil
		}
		rc, err := buildRealityConfig(cfg, dialContext)
		if err != nil {
			return nil, err
		}
		// Pre-measure the target site's post-handshake record lengths.
		// The fork waits for this data before completing authenticated
		// handshakes (it mimics the target's record structure).
		reality.DetectPostHandshakeRecordsLens(rc)
		s.rCfg = rc
	case "tls":
		cert, err := tls.LoadX509KeyPair(cfg.Inbound.TLS.Cert, cfg.Inbound.TLS.Key)
		if err != nil {
			return nil, fmt.Errorf("load cert: %w", err)
		}
		s.tlsCfg = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
			NextProtos:   []string{"h2"},
		}
	}
	return s, nil
}

// Serve accepts TCP connections until ctx is cancelled.
func (s *tunnelServer) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Inbound.Listen)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	log.Info("listening", "addr", s.cfg.Inbound.Listen, "mode", s.cfg.Inbound.Mode, "upstream", s.cfg.Upstream.Addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Warn("accept", "err", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if s.stats.Active() >= int64(s.cfg.Limits.MaxConnections) {
			conn.Close()
			continue
		}
		s.stats.AddActive(1)
		go func() {
			defer s.stats.AddActive(-1)
			s.handleConn(conn)
		}()
	}
}

func (s *tunnelServer) handleConn(raw net.Conn) {
	defer raw.Close()
	s.stats.IncTotalConns()
	hsTimeout := s.cfg.Limits.HandshakeTimeout.Duration
	raw.SetDeadline(time.Now().Add(hsTimeout))

	var conn net.Conn
	switch s.cfg.Inbound.Mode {
	case "reality":
		c, err := reality.Server(context.Background(), raw, s.rCfg)
		if err != nil {
			// The fork relays unauthenticated traffic to the target before
			// returning an error; count it as a relayed probe.
			s.stats.IncRelays()
			return
		}
		conn = c
	case "tls":
		tconn := tls.Server(raw, s.tlsCfg)
		if err := tconn.Handshake(); err != nil {
			return
		}
		conn = tconn
	}
	s.stats.IncAuthed()
	raw.SetDeadline(time.Time{})
	s.serveConn(conn)
}

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "genkey":
			runGenkey()
			return
		case "gencert":
			runGencert(os.Args[2:])
			return
		case "tlsserve":
			runTLSServe(os.Args[2:])
			return
		case "version", "-version", "--version":
			fmt.Println("naivereal-frontend", version)
			return
		}
	}
	path := "frontend.toml"
	if len(os.Args) >= 2 {
		path = os.Args[1]
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	configureLogging(cfg.LogLevel)
	srv, err := NewTunnelServer(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := srv.startStatus(ctx); err != nil {
			log.Warn("status server", "err", err)
		}
	}()
	if err := srv.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func configureLogging(level string) {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slogLevel}))
}
