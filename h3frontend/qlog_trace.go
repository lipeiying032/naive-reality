package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go"
	h3qlog "github.com/apernet/quic-go/http3/qlog"
	"github.com/apernet/quic-go/qlog"
	"github.com/apernet/quic-go/qlogwriter"
)

// sampledQLogTracerFactory writes compact per-connection qlogs. Full qlog
// records every packet and can dominate a single-vCPU VPS, changing the very
// behavior we are trying to diagnose. This tracer keeps only recovery and
// congestion events, plus packet loss/drop events, and samples
// recovery:metrics_updated at 10Hz.
type sampledQLogTracerFactory struct {
	dir string
}

func newSampledQLogTracerFactory(dir string) (*sampledQLogTracerFactory, error) {
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create qlog dir: %w", err)
	}
	return &sampledQLogTracerFactory{dir: dir}, nil
}

func (f *sampledQLogTracerFactory) Tracer(_ context.Context, isClient bool, connID quic.ConnectionID) qlogwriter.Trace {
	label := "server"
	if isClient {
		label = "client"
	}
	path := filepath.Join(f.dir, fmt.Sprintf("%s_%s.sqlog", connID, label))
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Error("create qlog file", "path", path, "err", err)
		return nil
	}
	wc := &flushWriteCloser{bufio.NewWriterSize(fh, 1<<16), fh}
	fs := qlogwriter.NewConnectionFileSeq(
		wc,
		isClient,
		connID,
		[]string{qlog.EventSchema, h3qlog.EventSchema},
	)
	go fs.Run()
	log.Debug("qlog trace created", "path", path)
	return &sampledTrace{inner: fs}
}

type sampledTrace struct {
	inner qlogwriter.Trace
}

func (t *sampledTrace) AddProducer() qlogwriter.Recorder {
	if t == nil || t.inner == nil {
		return nil
	}
	return &sampledRecorder{inner: t.inner.AddProducer()}
}

func (t *sampledTrace) SupportsSchemas(schema string) bool {
	return t != nil && t.inner != nil && t.inner.SupportsSchemas(schema)
}

const metricsSamplePeriod = 100 * time.Millisecond

type sampledRecorder struct {
	inner      qlogwriter.Recorder
	lastSample atomic.Int64
}

func (r *sampledRecorder) RecordEvent(e qlogwriter.Event) {
	if r == nil || r.inner == nil || e == nil {
		return
	}
	switch e.Name() {
	case "recovery:metrics_updated":
		now := time.Now()
		last := time.Unix(0, r.lastSample.Load())
		if now.Sub(last) < metricsSamplePeriod {
			return
		}
		r.lastSample.Store(now.UnixNano())
	case "recovery:packet_lost",
		"recovery:congestion_state_updated",
		"recovery:mtu_updated",
		"transport:packet_dropped",
		"transport:connection_started",
		"transport:connection_closed",
		"transport:parameters_set":
		// keep these events
	default:
		return
	}
	r.inner.RecordEvent(e)
}

func (r *sampledRecorder) Close() error {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.Close()
}

type flushWriteCloser struct {
	bw *bufio.Writer
	c  io.Closer
}

func (w *flushWriteCloser) Write(p []byte) (int, error) {
	return w.bw.Write(p)
}

func (w *flushWriteCloser) Close() error {
	return errors.Join(w.bw.Flush(), w.c.Close())
}
