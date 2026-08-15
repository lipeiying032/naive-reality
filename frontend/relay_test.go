package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestBuildH1Connect(t *testing.T) {
	h := http.Header{}
	h.Add("proxy-authorization", "Basic dXNlcjpwYXNz")
	h.Add("padding", "~~~~~")
	h.Add("padding-type-request", "1, 0")
	h.Add("connection", "keep-alive")
	req := string(buildH1Connect("example.com:443", h))
	if !strings.HasPrefix(req, "CONNECT example.com:443 HTTP/1.1\r\n") {
		t.Errorf("bad request line: %q", req)
	}
	for _, want := range []string{"host: example.com:443", "proxy-authorization: basic dxnlcjpwyxnz", "padding: ~~~~~", "padding-type-request: 1, 0"} {
		if !strings.Contains(strings.ToLower(req), want) {
			t.Errorf("missing %q in %q", want, req)
		}
	}
	if strings.Contains(strings.ToLower(req), "connection") {
		t.Errorf("hop-by-hop header leaked: %q", req)
	}
}

func TestCopyResponseHeaders(t *testing.T) {
	src := http.Header{}
	src.Add("padding", "xyz")
	src.Add("padding-type-reply", "1")
	src.Add("connection", "close")
	dst := http.Header{}
	copyResponseHeaders(dst, src)
	if dst.Get("padding") != "xyz" || dst.Get("padding-type-reply") != "1" {
		t.Errorf("headers missing: %v", dst)
	}
	if dst.Get("connection") != "" {
		t.Errorf("hop-by-hop header leaked: %v", dst)
	}
}

// startMockUpstream runs a minimal HTTP/1.1 CONNECT proxy that echoes bytes.
func startMockUpstream(t *testing.T) (addr string, echoPayload []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	echoPayload = []byte("hello-through-tunnel")
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil || req.Method != "CONNECT" {
					io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
					return
				}
				io.WriteString(c, "HTTP/1.1 200 OK\r\nPadding: abcdefg\r\nPadding-Type-Reply: 1\r\n\r\n")
				io.WriteString(c, string(echoPayload))
				io.Copy(c, br)
			}(c)
		}
	}()
	return ln.Addr().String(), echoPayload
}

func makeCert(t *testing.T) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPEM, keyPEM
}

// startTLSServer runs the tunnelServer in tls mode with a self-signed cert.
func startTLSServer(t *testing.T, upstream string) (string, *tls.Config) {
	t.Helper()
	_, certPEM, keyPEM := makeCert(t)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	cfg.Inbound.Listen = "127.0.0.1:0"
	cfg.Inbound.Mode = "tls"
	cfg.Inbound.TLS.Cert = certPath
	cfg.Inbound.TLS.Key = keyPath
	cfg.Upstream.Addr = upstream
	cfg.Limits.MaxConnections = 64
	cfg.Limits.MaxRelays = 8
	cfg.Limits.HandshakeTimeout.Duration = 10 * time.Second
	cfg.Limits.IdleTimeout.Duration = 60 * time.Second

	srv, err := NewTunnelServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(c)
		}
	}()
	clientCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
	}
	return ln.Addr().String(), clientCfg
}

func TestTLSModeH2ConnectRelay(t *testing.T) {
	upstream, echo := startMockUpstream(t)
	addr, clientTLS := startTLSServer(t, upstream)

	conn, err := tls.Dial("tcp", addr, clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.ConnectionState().NegotiatedProtocol; got != "h2" {
		t.Fatalf("ALPN = %q, want h2", got)
	}
	tr := newTestH2Client(t, conn)
	resp := tr.connect(t, "example.com:443", http.Header{
		"proxy-authorization":  []string{"Basic dXNlcjpwYXNz"},
		"padding":              []string{"~~~~"},
		"padding-type-request": []string{"1, 0"},
	})
	if resp.status != 200 {
		t.Fatalf("connect status = %d", resp.status)
	}
	if resp.header.Get("padding-type-reply") != "1" {
		t.Errorf("padding-type-reply = %q", resp.header.Get("padding-type-reply"))
	}
	buf := make([]byte, len(echo))
	if _, err := io.ReadFull(tr.body(), buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(echo) {
		t.Errorf("echo = %q, want %q", buf, echo)
	}
	if _, err := tr.body().Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(tr.body(), got); err != nil {
		t.Fatalf("read echo2: %v", err)
	}
	if string(got) != "ping" {
		t.Errorf("echo2 = %q", got)
	}
}

// testH2Client is a minimal HTTP/2 client driving one CONNECT stream (id 1).
type testH2Client struct {
	t     *testing.T
	conn  net.Conn
	fr    *http2.Framer
	wmu   sync.Mutex
	pr    *io.PipeReader
	pw    *io.PipeWriter
	resp  chan testH2Resp
}

type testH2Resp struct {
	status int
	header http.Header
}

func newTestH2Client(t *testing.T, conn net.Conn) *testH2Client {
	t.Helper()
	fr := http2.NewFramer(conn, conn)
	pr, pw := io.Pipe()
	c := &testH2Client{t: t, conn: conn, fr: fr, pr: pr, pw: pw, resp: make(chan testH2Resp, 1)}
	io.WriteString(conn, http2.ClientPreface)
	if err := fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	go c.readLoop()
	return c
}

func (c *testH2Client) readLoop() {
	status := 0
	header := http.Header{}
	for {
		f, err := c.fr.ReadFrame()
		if err != nil {
			c.pw.CloseWithError(err)
			return
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				c.wmu.Lock()
				c.fr.WriteSettingsAck()
				c.wmu.Unlock()
			}
		case *http2.PingFrame:
			c.wmu.Lock()
			c.fr.WritePing(true, f.Data)
			c.wmu.Unlock()
		case *http2.HeadersFrame:
			hdrs, err := hpack.NewDecoder(4096, nil).DecodeFull(f.HeaderBlockFragment())
			if err != nil {
				c.pw.CloseWithError(err)
				return
			}
			for _, h := range hdrs {
				if h.Name == ":status" {
					fmt.Sscanf(h.Value, "%d", &status)
				} else if !strings.HasPrefix(h.Name, ":") {
					header.Add(h.Name, h.Value)
				}
			}
			if f.StreamEnded() {
				select {
				case c.resp <- testH2Resp{status, header}:
				default:
				}
				c.pw.Close()
				return
			}
			select {
			case c.resp <- testH2Resp{status, header}:
			default:
			}
		case *http2.DataFrame:
			if f.StreamID == 1 {
				if _, err := c.pw.Write(f.Data()); err != nil {
					return
				}
				if f.StreamEnded() {
					c.pw.Close()
					return
				}
			}
		case *http2.GoAwayFrame:
			return
		}
	}
}

func (c *testH2Client) connect(t *testing.T, authority string, header http.Header) testH2Resp {
	t.Helper()
	var hbuf bytes.Buffer
	enc := hpack.NewEncoder(&hbuf)
	// Classic CONNECT (RFC 7540 8.3): only :method and :authority are allowed;
	// the Go http2 server rejects :scheme/:path here with PROTOCOL_ERROR.
	enc.WriteField(hpack.HeaderField{Name: ":method", Value: "CONNECT"})
	enc.WriteField(hpack.HeaderField{Name: ":authority", Value: authority})
	for k, vs := range header {
		for _, v := range vs {
			enc.WriteField(hpack.HeaderField{Name: strings.ToLower(k), Value: v})
		}
	}
	c.wmu.Lock()
	err := c.fr.WriteHeaders(http2.HeadersFrameParam{
		StreamID:      1,
		BlockFragment: hbuf.Bytes(),
		EndStream:     false,
		EndHeaders:    true,
	})
	c.wmu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case resp := <-c.resp:
		return resp
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for response headers")
		return testH2Resp{}
	}
}

// body is the bidirectional CONNECT stream.
func (c *testH2Client) body() io.ReadWriteCloser {
	return &testH2Body{c: c}
}

type testH2Body struct {
	c *testH2Client
}

func (b *testH2Body) Read(p []byte) (int, error) { return b.c.pr.Read(p) }

func (b *testH2Body) Write(p []byte) (int, error) {
	b.c.wmu.Lock()
	defer b.c.wmu.Unlock()
	if err := b.c.fr.WriteData(1, false, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (b *testH2Body) Close() error {
	b.c.wmu.Lock()
	defer b.c.wmu.Unlock()
	return b.c.fr.WriteData(1, true, nil)
}
