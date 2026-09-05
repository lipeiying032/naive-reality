package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"github.com/apernet/quic-go/qlogwriter"

	"naivereal/h3frontend/internal/congestion"
)

var log = slog.New(slog.NewTextHandler(os.Stderr, nil))

func main() {
	path := "h3frontend.toml"
	checkOnly := false
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "check":
			if len(os.Args) != 3 {
				fmt.Fprintln(os.Stderr, "usage: h3frontend check <config.toml>")
				os.Exit(2)
			}
			path = os.Args[2]
			checkOnly = true
		case "-h", "--help":
			fmt.Println("usage: h3frontend [config.toml] | check <config.toml>\nDefault: origin mode with an owned TLS certificate. See docs/h3-origin.md.")
			return
		case "genkey":
			fmt.Fprintln(os.Stderr, "H3 now uses an owned TLS certificate; REALITY genkey is only available in the TCP frontend")
			os.Exit(1)
		default:
			path = os.Args[1]
		}
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if checkOnly {
		if _, err := tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key); err != nil {
			fmt.Fprintln(os.Stderr, "certificate:", err)
			os.Exit(1)
		}
		fmt.Println("configuration and TLS key pair are valid; no listeners started")
		return
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
	// All listeners and accepted connections belong to this invocation, including
	// when startup fails or one of the optional website listeners fails.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if cfg.Mode != "origin" && cfg.Mode != "tls" {
		return fmt.Errorf("unsupported mode %q; use origin with an owned TLS certificate", cfg.Mode)
	}
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
	qlogFactory, err := newSampledQLogTracerFactory(os.Getenv("QLOGDIR"))
	if err != nil {
		return fmt.Errorf("qlog: %w", err)
	}
	var tracer func(context.Context, bool, quic.ConnectionID) qlogwriter.Trace
	if qlogFactory != nil {
		tracer = qlogFactory.Tracer
	}
	quicConf := &quic.Config{
		InitialPacketSize:              cfg.QUIC.InitialPacketSize,
		InitialStreamReceiveWindow:     cfg.QUIC.InitialStreamReceiveWindow,
		MaxStreamReceiveWindow:         cfg.QUIC.MaxStreamReceiveWindow,
		InitialConnectionReceiveWindow: cfg.QUIC.InitialConnectionReceiveWindow,
		MaxConnectionReceiveWindow:     cfg.QUIC.MaxConnectionReceiveWindow,
		MaxIdleTimeout:                 maxIdle,
		MaxIncomingStreams:             cfg.QUIC.MaxIncomingStreams,
		DisablePathMTUDiscovery:        cfg.QUIC.DisablePathMTUDiscovery,
		DisablePathManager:             cfg.QUIC.DisablePathManager,
		Allow0RTT:                      false,
		// QLOGDIR enables sampled per-connection qlogs (loss/cwnd/RTT only).
		Tracer: tracer,
	}

	addr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve listen: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	_ = udpConn.SetReadBuffer(4 << 20)
	_ = udpConn.SetWriteBuffer(4 << 20)
	defer udpConn.Close()

	transport := &quic.Transport{Conn: udpConn, DisableGSO: cfg.QUIC.DisableGSO}
	defer transport.Close()
	listener, err := transport.Listen(tlsConf, quicConf)
	if err != nil {
		return fmt.Errorf("quic listen: %w", err)
	}
	defer listener.Close()

	dialer := net.Dialer{Timeout: 10 * time.Second}
	handler := &relayHandler{
		upstream: cfg.Upstream.Addr,
		dialer:   dialer,
	}
	var h3Handler http.Handler = handler
	var website http.Handler
	if cfg.Mode == "origin" {
		origin, closeRoot, err := newOriginHandler(cfg.Origin, handler)
		if err != nil {
			return fmt.Errorf("open origin website: %w", err)
		}
		defer closeRoot()
		h3Handler = origin
		website = origin.website
	}
	h3srv := &http3.Server{Handler: h3Handler}
	defer h3srv.Close()

	// The optional TCP listener serves the same website and advertises this UDP
	// endpoint. It uses the same certificate, but only H3 carries proxy CONNECT.
	// No listener or certificate selection depends on proxy credentials.
	websiteErr := make(chan error, 1)
	if website != nil && cfg.Origin.TCPListen != "" {
		ln, err := net.Listen("tcp", cfg.Origin.TCPListen)
		if err != nil {
			return fmt.Errorf("origin tcp listen: %w", err)
		}
		defer ln.Close()
		altSvc := fmt.Sprintf(`h3=":%d"; ma=86400`, udpConn.LocalAddr().(*net.UDPAddr).Port)
		websrv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Alt-Svc", altSvc)
				website.ServeHTTP(w, r)
			}),
			TLSConfig: &tls.Config{
				Certificates: tlsConf.Certificates,
				MinVersion:   tls.VersionTLS13,
			},
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       maxIdle,
		}
		defer websrv.Close()
		go func() {
			err := websrv.ServeTLS(ln, "", "")
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				websiteErr <- err
				cancel()
			}
		}()
		log.Info("origin website listening", "addr", ln.Addr())
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
		_ = udpConn.Close()
	}()

	log.Info("listening", "addr", udpConn.LocalAddr(), "mode", cfg.Mode, "congestion", cfg.Congestion.Type, "bbr_profile", cfg.Congestion.BBRProfile)
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			select {
			case err := <-websiteErr:
				return fmt.Errorf("origin website: %w", err)
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		if cfg.Congestion.Type == "bbr" {
			if err := congestion.UseBBR(conn, cfg.Congestion.BBRProfile); err != nil {
				log.Error("replace congestion controller", "remote", conn.RemoteAddr(), "err", err)
				_ = conn.CloseWithError(0, err.Error())
				continue
			}
		}
		remote := conn.RemoteAddr()
		acceptedAt := time.Now()
		log.Debug("quic conn accepted", "remote", remote)
		go func() {
			defer conn.CloseWithError(0, "")
			err := h3srv.ServeQUICConn(conn)
			if err != nil {
				log.Warn("quic conn closed with error", "remote", remote, "duration", time.Since(acceptedAt), "err", err)
			} else {
				log.Debug("quic conn closed", "remote", remote, "duration", time.Since(acceptedAt))
			}
		}()
	}
}
