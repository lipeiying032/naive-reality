package main

import (
	"net/http"
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
