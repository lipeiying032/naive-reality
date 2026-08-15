package main

import (
	"bufio"
	"context"
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
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
)

func startH3MockUpstream(t *testing.T) (addr string) {
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
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil || req.Method != http.MethodConnect {
					_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nPadding: abc\r\nPadding-Type-Reply: 1\r\n\r\n")
				_, _ = io.Copy(c, br)
			}(c)
		}
	}()
	return ln.Addr().String()
}

func makeH3TestCert(t *testing.T, dnsName string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestH3ConnectRelayIntegration(t *testing.T) {
	upstream := startH3MockUpstream(t)
	certPath, keyPath := makeH3TestCert(t, "example.com")

	udpProbe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	serverAddr := udpProbe.LocalAddr().String()
	_ = udpProbe.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "h3frontend.toml")
	cfg := fmt.Sprintf(`
listen = %q
[tls]
cert = %q
key = %q
[quic]
initStreamReceiveWindow = 8388608
maxStreamReceiveWindow = 8388608
initConnReceiveWindow = 20971520
maxConnReceiveWindow = 20971520
[congestion]
type = "bbr"
bbrProfile = "standard"
[upstream]
addr = %q
`, serverAddr, certPath, keyPath, upstream)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- serve(ctx, c) }()

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodConnect, "https://example.com:443", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("padding", "~~~~~")

	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h3"},
		},
		Dial: func(ctx context.Context, _ string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			return quic.DialAddr(ctx, serverAddr, tlsCfg, cfg)
		},
	}
	defer tr.Close()

	var resp *http.Response
	for i := 0; i < 20; i++ {
		resp, err = tr.RoundTrip(req)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if _, err := pw.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("echo = %q", got)
	}
	_ = pw.Close()
	_ = resp.Body.Close()
	cancel()
	select {
	case err := <-serveErr:
		if err != nil && ctx.Err() == nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}
