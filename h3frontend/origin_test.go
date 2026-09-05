package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func originTestConfig(t *testing.T) OriginConfig {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("owned website"), 0o600); err != nil {
		t.Fatal(err)
	}
	return OriginConfig{WebRoot: root, Username: "user", Password: "test-password"}
}

func originTestAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("user:test-password"))
}

func TestOriginAuthorizationIsPerRequest(t *testing.T) {
	proxyCalls := 0
	h, closeRoot, err := newOriginHandler(originTestConfig(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer closeRoot()
	// All requests share an address. Successful authorization must not authorize
	// the following requests, including one with duplicate credentials.
	cases := []struct {
		name, method string
		auth         []string
		proxy        bool
	}{
		{"authorized", http.MethodConnect, []string{originTestAuth()}, true},
		{"missing after success", http.MethodConnect, nil, false},
		{"wrong password", http.MethodConnect, []string{"Basic dXNlcjp3cm9uZw=="}, false},
		{"duplicate header", http.MethodConnect, []string{originTestAuth(), originTestAuth()}, false},
		{"malformed", http.MethodConnect, []string{"Basic %"}, false},
		{"origin authorization header", http.MethodConnect, nil, false},
		{"GET with credentials", http.MethodGet, []string{originTestAuth()}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "https://site.test/", nil)
			r.RemoteAddr = "127.0.0.1:34567"
			r.Header["Proxy-Authorization"] = tc.auth
			r.Header.Set("Authorization", originTestAuth()) // must not authorize a proxy
			before := proxyCalls
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if got := proxyCalls > before; got != tc.proxy {
				t.Fatalf("proxy called = %v, want %v", got, tc.proxy)
			}
			if tc.method == http.MethodGet && (w.Code != http.StatusOK || w.Body.String() != "owned website") {
				t.Fatalf("website response: %d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestOriginWebsiteConfinesSymlinks(t *testing.T) {
	cfg := originTestConfig(t)
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("outside root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cfg.WebRoot, "escape.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	h, closeRoot, err := newOriginHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRoot()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://site.test/escape.txt", nil))
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "outside root") {
		t.Fatalf("website escaped its root: %d %q", w.Code, w.Body.String())
	}
}

func TestOriginConfigRejectsAmbiguousMigration(t *testing.T) {
	cfg := originTestConfig(t)
	cert, key := makeH3TestCert(t, "site.test")
	base := fmt.Sprintf("mode = 'origin'\n[tls]\ncert = %q\nkey = %q\n[origin]\nweb_root = %q\n", cert, key, cfg.WebRoot)
	cases := []struct{ name, extra, want string }{
		{"missing credentials", "", "origin.username"},
		{"username colon", "username = 'bad:name'\npassword = 'pass'\n", "invalid HTTP Basic"},
		{"legacy section", "username = 'user'\npassword = 'pass'\n[reality]\n", "REALITY-over-QUIC has been removed"},
		{"bad TCP listener", "username = 'user'\npassword = 'pass'\ntcp_listen = 'invalid'\n", "origin.tcp_listen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(base+tc.extra), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadConfig(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOriginIsDefaultAndRealityModeIsRejected(t *testing.T) {
	cert, key := makeH3TestCert(t, "site.test")
	cfg := Config{TLS: TLSConfig{Cert: cert, Key: key}, Origin: originTestConfig(t)}
	if err := cfg.validateAndFill(); err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "origin" {
		t.Fatalf("default mode = %q", cfg.Mode)
	}
	legacy := Config{Mode: "reality"}
	if err := legacy.validateAndFill(); err == nil || !strings.Contains(err.Error(), "has been removed") {
		t.Fatalf("legacy mode did not produce a migration error: %v", err)
	}
}
