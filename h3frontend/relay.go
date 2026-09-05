package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/apernet/quic-go/http3"
)

// relayHandler terminates standard HTTP/3 CONNECT requests and relays them to
// the official naive server over HTTP/1.1 CONNECT. It deliberately has no
// REALITY dependency and does not inspect or parse REALITY flags.
type relayHandler struct {
	upstream string
	dialer   net.Dialer
	nextID   atomic.Uint64
}

type copyResult struct {
	dir     string
	bytes   int64
	err     error
	elapsed time.Duration
}

func (h *relayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := h.nextID.Add(1)
	host := r.Host
	start := time.Now()
	if r.Method != http.MethodConnect {
		log.Debug("h3 non-connect request", "id", rid, "method", r.Method, "host", host, "remote", r.RemoteAddr)
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "<html><head><title>404 Not Found</title></head><body>404 Not Found</body></html>")
		return
	}
	log.Debug("h3 connect begin", "id", rid, "host", host, "remote", r.RemoteAddr)

	up, err := h.dialer.Dial("tcp", h.upstream)
	if err != nil {
		log.Warn("h3 upstream dial failed", "id", rid, "host", host, "err", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer up.Close()

	if _, err := up.Write(buildH1Connect(r.Host, r.Header)); err != nil {
		log.Warn("h3 upstream write failed", "id", rid, "host", host, "err", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	ubr := bufio.NewReader(up)
	resp, err := http.ReadResponse(ubr, r)
	if err != nil {
		log.Warn("h3 upstream response failed", "id", rid, "host", host, "err", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	copyResponseHeaders(w.Header(), resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Warn("h3 upstream returned non-2xx", "id", rid, "host", host, "status", resp.StatusCode)
		w.WriteHeader(resp.StatusCode)
		return
	}
	w.WriteHeader(http.StatusOK)

	streamer, ok := w.(http3.HTTPStreamer)
	if !ok {
		log.Error("h3 response writer is not an HTTPStreamer", "id", rid, "host", host)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	stream := streamer.HTTPStream()
	log.Debug("h3 connect upstream ok", "id", rid, "host", host, "upstream", up.RemoteAddr(), "stream_id", stream.StreamID(), "elapsed", time.Since(start))

	copyStarted := time.Now()
	done := make(chan copyResult, 2)
	go func() {
		n, copyErr := io.Copy(up, stream) // client -> upstream
		closeErr := error(nil)
		if tc, ok := up.(*net.TCPConn); ok {
			closeErr = tc.CloseWrite()
		}
		done <- copyResult{dir: "client->upstream", bytes: n, err: errors.Join(copyErr, closeErr), elapsed: time.Since(copyStarted)}
	}()
	go func() {
		n, copyErr := io.Copy(stream, ubr) // upstream -> client
		closeErr := stream.Close()
		done <- copyResult{dir: "upstream->client", bytes: n, err: errors.Join(copyErr, closeErr), elapsed: time.Since(copyStarted)}
	}()

	first := <-done
	log.Debug("h3 tunnel first direction closed", "id", rid, "host", host, "dir", first.dir, "bytes", first.bytes, "err", first.err, "elapsed", first.elapsed)
	_ = up.Close()
	_ = stream.Close()
	select {
	case second := <-done:
		log.Debug("h3 tunnel second direction closed", "id", rid, "host", host, "dir", second.dir, "bytes", second.bytes, "err", second.err, "elapsed", second.elapsed)
	case <-time.After(5 * time.Second):
		log.Warn("h3 tunnel second direction still open after 5s", "id", rid, "host", host)
	}
	log.Debug("h3 tunnel closed", "id", rid, "host", host, "elapsed", time.Since(start))
}

func buildH1Connect(authority string, header http.Header) []byte {
	var b strings.Builder
	b.WriteString("CONNECT ")
	b.WriteString(authority)
	b.WriteString(" HTTP/1.1\r\n")
	b.WriteString("Host: ")
	b.WriteString(authority)
	b.WriteString("\r\n")
	for name, vals := range header {
		n := strings.ToLower(name)
		if isHopByHop(n) {
			continue
		}
		for _, v := range vals {
			b.WriteString(n)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\r\n")
	return []byte(b.String())
}

func copyResponseHeaders(dst, src http.Header) {
	for name, vals := range src {
		n := strings.ToLower(name)
		if isHopByHop(n) || n == "content-length" {
			continue
		}
		for _, v := range vals {
			dst.Add(n, v)
		}
	}
}

func isHopByHop(name string) bool {
	switch name {
	case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "te", "trailer", "upgrade":
		return true
	}
	return false
}
