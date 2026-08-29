package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildH1Connect(t *testing.T) {
	h := http.Header{}
	h.Add("proxy-authorization", "Basic dXNlcjpwYXNz")
	h.Add("padding", "~~~~~")
	h.Add("padding-type-request", "1, 0")
	h.Add("connection", "keep-alive")
	req := string(buildH1Connect("example.com:443", h))
	if !strings.HasPrefix(req, "CONNECT example.com:443 HTTP/1.1\r\n") {
		t.Fatalf("bad request line: %q", req)
	}
	for _, want := range []string{"host: example.com:443", "proxy-authorization: basic dxnlcjpwyxnz", "padding: ~~~~~", "padding-type-request: 1, 0"} {
		if !strings.Contains(strings.ToLower(req), want) {
			t.Fatalf("missing %q in %q", want, req)
		}
	}
	if strings.Contains(strings.ToLower(req), "connection") {
		t.Fatal("hop-by-hop header leaked")
	}
}

func TestRelayFallbackProxiesNonConnectRequests(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" || r.URL.RawQuery != "q=1" {
			t.Errorf("unexpected destination request: %s", r.URL)
		}
		if got := r.Header.Get("Proxy-Authorization"); got != "" {
			t.Errorf("proxy authorization leaked to destination")
		}
		w.Header().Set("X-Test", "yes")
		_, _ = io.WriteString(w, "destination response")
	}))
	defer origin.Close()

	handler := &relayHandler{
		fallbackHost:   origin.Listener.Addr().String(),
		fallbackClient: origin.Client(),
	}
	req := httptest.NewRequest(http.MethodGet, "https://naivereal.test/status?q=1", nil)
	req.Header.Set("Proxy-Authorization", "Basic should-not-forward")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Test") != "yes" {
		t.Fatalf("destination headers missing: %v", rec.Header())
	}
	if rec.Body.String() != "destination response" {
		t.Fatalf("body = %q", rec.Body.String())
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
		t.Fatalf("headers missing: %v", dst)
	}
	if dst.Get("connection") != "" {
		t.Fatal("hop-by-hop header leaked")
	}
}
