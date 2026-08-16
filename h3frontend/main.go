package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"

	"naivereal/h3frontend/internal/congestion"
)

var log = slog.New(slog.NewTextHandler(os.Stderr, nil))

func main() {
	path := "h3frontend.toml"
	if len(os.Args) >= 2 {
		path = os.Args[1]
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	setLogLevel(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, cfg); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func setLogLevel(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func serve(ctx context.Context, cfg *Config) error {
	cert, err := tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key)
	if err != nil {
		return fmt.Errorf("load cert: %w", err)
	}
	tlsConf := http3.ConfigureTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})

	maxIdle, err := time.ParseDuration(cfg.QUIC.MaxIdleTimeout)
	if err != nil {
		return err
	}
	quicConf := &quic.Config{
		InitialStreamReceiveWindow:     cfg.QUIC.InitialStreamReceiveWindow,
		MaxStreamReceiveWindow:         cfg.QUIC.MaxStreamReceiveWindow,
		InitialConnectionReceiveWindow: cfg.QUIC.InitialConnectionReceiveWindow,
		MaxConnectionReceiveWindow:     cfg.QUIC.MaxConnectionReceiveWindow,
		MaxIdleTimeout:                 maxIdle,
		MaxIncomingStreams:             cfg.QUIC.MaxIncomingStreams,
		DisablePathMTUDiscovery:        cfg.QUIC.DisablePathMTUDiscovery,
		Allow0RTT:                      true,
	}

	addr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve listen: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer udpConn.Close()

	transport := &quic.Transport{Conn: udpConn, DisableGSO: cfg.QUIC.DisableGSO}
	defer transport.Close()
	listener, err := transport.Listen(tlsConf, quicConf)
	if err != nil {
		return fmt.Errorf("quic listen: %w", err)
	}
	defer listener.Close()

	h3srv := &http3.Server{
		Handler: &relayHandler{
			upstream: cfg.Upstream.Addr,
			dialer:   net.Dialer{Timeout: 10 * time.Second},
		},
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
		_ = udpConn.Close()
	}()

	log.Info("listening", "addr", udpConn.LocalAddr(), "congestion", cfg.Congestion.Type, "bbr_profile", cfg.Congestion.BBRProfile)
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		if err := congestion.UseBBR(conn, cfg.Congestion.BBRProfile); err != nil {
			_ = conn.CloseWithError(0, err.Error())
			continue
		}
		go func() {
			if err := h3srv.ServeQUICConn(conn); err != nil {
				log.Debug("serve quic conn", "err", err)
			}
		}()
	}
}
