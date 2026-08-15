package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/apernet/quic-go/http3"
)

// relayHandler terminates standard HTTP/3 CONNECT requests and relays them to
// the official naive server over HTTP/1.1 CONNECT. It deliberately has no
// REALITY dependency and does not inspect or parse REALITY flags.
type relayHandler struct {
	upstream string
	dialer   net.Dialer
}

func (h *relayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "<html><head><title>404 Not Found</title></head><body>404 Not Found</body></html>")
		return
	}

	up, err := h.dialer.Dial("tcp", h.upstream)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer up.Close()

	if _, err := up.Write(buildH1Connect(r.Host, r.Header)); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	ubr := bufio.NewReader(up)
	resp, err := http.ReadResponse(ubr, r)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	copyResponseHeaders(w.Header(), resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		w.WriteHeader(resp.StatusCode)
		return
	}
	w.WriteHeader(http.StatusOK)

	streamer, ok := w.(http3.HTTPStreamer)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	stream := streamer.HTTPStream()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(up, stream) // client -> upstream
		if tc, ok := up.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(stream, ubr) // upstream -> client
		_ = stream.Close()
		done <- struct{}{}
	}()
	<-done
	_ = up.Close()
	_ = stream.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
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
