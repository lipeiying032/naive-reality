package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Stats holds counters shared across connections.
type Stats struct {
	totalConns atomic.Int64
	authed     atomic.Int64
	relays     atomic.Int64
	tunnels    atomic.Int64
	active     atomic.Int64
}

func (s *Stats) IncTotalConns() { s.totalConns.Add(1) }
func (s *Stats) IncAuthed()     { s.authed.Add(1) }
func (s *Stats) IncRelays()     { s.relays.Add(1) }
func (s *Stats) IncTunnels()    { s.tunnels.Add(1) }
func (s *Stats) AddActive(n int64) {
	s.active.Add(n)
}
func (s *Stats) Active() int64 { return s.active.Load() }

// startStatus serves a small JSON stats endpoint, guarded by a token.
func (s *tunnelServer) startStatus(ctx context.Context) error {
	if s.cfg.Status.HTTP == "" {
		return nil
	}
	srv := &http.Server{
		Addr:              s.cfg.Status.HTTP,
		Handler:           s.statusHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Info("status endpoint", "addr", s.cfg.Status.HTTP)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *tunnelServer) statusHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		const bearerPrefix = "Bearer "
		auth := r.Header.Get("Authorization")
		provided := ""
		if strings.HasPrefix(auth, bearerPrefix) {
			provided = strings.TrimPrefix(auth, bearerPrefix)
		}
		if len(provided) != len(s.cfg.Status.Token) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.Status.Token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprintf(w, "{\"total_conns\":%d,\"authed\":%d,\"relays\":%d,\"tunnels\":%d,\"active\":%d}",
			s.stats.totalConns.Load(), s.stats.authed.Load(), s.stats.relays.Load(), s.stats.tunnels.Load(), s.stats.active.Load())
	})
	return mux
}
