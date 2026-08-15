package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// serveH2 runs an HTTP/2 server over an authenticated connection.
func (s *tunnelServer) serveH2(conn net.Conn) {
	h2s := &http2.Server{
		IdleTimeout: s.cfg.Limits.IdleTimeout.Duration,
	}
	h2s.ServeConn(conn, &http2.ServeConnOpts{
		Handler: http.HandlerFunc(s.handleRequest),
	})
}

func (s *tunnelServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "<html><head><title>404 Not Found</title></head><body>404 Not Found</body></html>")
		return
	}

	up, err := net.DialTimeout("tcp", s.cfg.Upstream.Addr, 10*time.Second)
	if err != nil {
		log.Warn("upstream dial", "err", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer up.Close()

	if _, err := up.Write(buildH1Connect(r.Host, r.Header)); err != nil {
		log.Warn("upstream write", "err", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	br := bufio.NewReader(up)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		log.Warn("upstream response", "err", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	copyResponseHeaders(w.Header(), resp.Header)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(resp.StatusCode)
		return
	}
	w.(http.Flusher).Flush()

	s.stats.IncTunnels()
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(up, r.Body) // client -> upstream
		if tc, ok := up.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		// The http2 ResponseWriter buffers DATA frames; flush after every
		// write so tunnel bytes reach the client immediately.
		io.Copy(flushWriter{w}, br)
		done <- struct{}{}
	}()
	<-done
	up.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// buildH1Connect renders an HTTP/1.1 CONNECT request from an h2 CONNECT
// request, preserving the naive negotiation headers.
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

// copyResponseHeaders maps h1 upstream response headers to the h2 client.
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

// flushWriter flushes the underlying HTTP/2 stream after every write.
type flushWriter struct {
	w http.ResponseWriter
}

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fw, ok := f.w.(http.Flusher); ok {
		fw.Flush()
	}
	return n, err
}

func isHopByHop(name string) bool {
	switch name {
	case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "te", "trailer", "upgrade":
		return true
	}
	return false
}
