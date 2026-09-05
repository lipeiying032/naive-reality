package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
)

func startOriginTestUpstream(t *testing.T, calls *atomic.Int32) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				br := bufio.NewReader(c)
				r, err := http.ReadRequest(br)
				if err != nil || r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != originTestAuth() {
					_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
					return
				}
				calls.Add(1)
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nPadding-Type-Reply: 1\r\n\r\n")
				_, _ = io.Copy(c, br)
			}()
		}
	}()
	return ln.Addr().String()
}

func startOriginTestServer(t *testing.T, upstream string) (string, string, *tls.Config) {
	t.Helper()
	cert, key := makeH3TestCert(t, "site.test")
	// serve owns its listeners; reserve distinct OS-selected addresses only for
	// the short interval needed to construct its configuration.
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpAddr := udp.LocalAddr().String()
	_ = udp.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr := tcp.Addr().String()
	_ = tcp.Close()
	cfg := &Config{Mode: "origin", Listen: udpAddr, TLS: TLSConfig{Cert: cert, Key: key}, Origin: originTestConfig(t), Upstream: UpstreamConfig{Addr: upstream}}
	cfg.Origin.TCPListen = tcpAddr
	if err := cfg.validateAndFill(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("origin serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("origin server did not stop")
		}
	})
	data, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		t.Fatal("test certificate not loaded")
	}
	clientTLS := &tls.Config{RootCAs: roots, ServerName: "site.test", NextProtos: []string{"h3"}}
	// Readiness checks complete a *verified* handshake; no insecure bypass.
	for i := 0; i < 30; i++ {
		conn, err := quic.DialAddr(ctx, udpAddr, clientTLS, &quic.Config{HandshakeIdleTimeout: 100 * time.Millisecond})
		if err == nil {
			_ = conn.CloseWithError(0, "")
			return udpAddr, tcpAddr, clientTLS
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("origin listener did not become ready")
	return "", "", nil
}

func TestOriginH3ConnectionAndRequestIsolation(t *testing.T) {
	var calls atomic.Int32
	addr, _, clientTLS := startOriginTestServer(t, startOriginTestUpstream(t, &calls))
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	qt := &quic.Transport{Conn: udp}
	defer qt.Close()
	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	var dials atomic.Int32
	newClient := func() *http3.Transport {
		return &http3.Transport{
			TLSClientConfig: clientTLS,
			Dial: func(ctx context.Context, _ string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
				dials.Add(1)
				return qt.Dial(ctx, remote, tc, qc)
			},
		}
	}
	tr := newClient()
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	roundTrip := func(tr *http3.Transport, method, auth string, body io.Reader) *http.Response {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, method, "https://site.test:443/", body)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			r.Header.Set("Proxy-Authorization", auth)
		}
		resp, err := tr.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	website := roundTrip(tr, http.MethodGet, "", nil)
	data, err := io.ReadAll(website.Body)
	_ = website.Body.Close()
	if err != nil || string(data) != "owned website" || website.ProtoMajor != 3 {
		t.Fatalf("website: %q %s %v", data, website.Proto, err)
	}
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	resp := roundTrip(tr, http.MethodConnect, originTestAuth(), pr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %s", resp.Status)
	}
	if _, err := pw.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("tunnel echo: %q %v", buf, err)
	}
	_ = pw.Close()
	_ = resp.Body.Close()
	for _, auth := range []string{"", "Basic dXNlcjp3cm9uZw=="} {
		resp := roundTrip(tr, http.MethodConnect, auth, nil)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			t.Fatalf("unauthorized CONNECT succeeded: %s", resp.Status)
		}
	}
	if dials.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("one H3 connection, one authorized request expected: dials=%d upstream=%d", dials.Load(), calls.Load())
	}
	// Open another QUIC connection on exactly the same client UDP socket while
	// the first is still live. Authorization must not cross the connection.
	other := newClient()
	defer other.Close()
	resp = roundTrip(other, http.MethodConnect, "", nil)
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 || calls.Load() != 1 || dials.Load() != 2 {
		t.Fatalf("authorization crossed connections: %s dials=%d upstream=%d", resp.Status, dials.Load(), calls.Load())
	}
}

func TestOriginStandardCertificateVerificationAndWebsiteDiscovery(t *testing.T) {
	var calls atomic.Int32
	addr, tcpAddr, clientTLS := startOriginTestServer(t, startOriginTestUpstream(t, &calls))
	for _, name := range []string{"untrusted certificate", "wrong hostname"} {
		t.Run(name, func(t *testing.T) {
			tc := clientTLS.Clone()
			if name == "untrusted certificate" {
				tc.RootCAs = x509.NewCertPool()
			} else {
				tc.ServerName = "other.test"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, err := quic.DialAddr(ctx, addr, tc, nil)
			if err == nil {
				_ = conn.CloseWithError(0, "")
				t.Fatal("invalid certificate accepted")
			}
			if !strings.Contains(err.Error(), "x509:") {
				t.Fatalf("failed for a reason other than certificate verification: %v", err)
			}
		})
	}
	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: clientTLS.RootCAs},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", tcpAddr)
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	resp, err := client.Get("https://site.test/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	_, port, _ := net.SplitHostPort(addr)
	want := fmt.Sprintf(`h3=":%s";`, port)
	if err != nil || string(data) != "owned website" || !strings.HasPrefix(resp.Header.Get("Alt-Svc"), want) || resp.ProtoMajor != 2 {
		t.Fatalf("HTTPS website discovery: %q %q %s %v", data, resp.Header.Get("Alt-Svc"), resp.Proto, err)
	}
}
