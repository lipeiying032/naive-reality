package entry

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"naivereal/tui/internal/config"
	"naivereal/tui/internal/coremgr"
	"naivereal/tui/internal/stats"
)

// findNaiveExe locates the official naive.exe test binary near the repo.
func findNaiveExe(t *testing.T) string {
	t.Helper()
	if candidate := os.Getenv("NAIVE_TEST_EXE"); candidate != "" {
		path, err := filepath.Abs(candidate)
		if err != nil {
			t.Fatalf("resolve NAIVE_TEST_EXE: %v", err)
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
		t.Fatalf("NAIVE_TEST_EXE does not name a file: %s", path)
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		matches, _ := filepath.Glob(filepath.Join(dir, "tools", "naive-win", "*", "naive.exe"))
		if len(matches) > 0 {
			return matches[0]
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if os.Getenv("NAIVEREAL_REQUIRE_CORE_E2E") == "1" {
		t.Fatal("naive.exe not found while NAIVEREAL_REQUIRE_CORE_E2E=1")
	}
	t.Skip("naive.exe not found near repo (set NAIVE_TEST_EXE to require this test)")
	return ""
}

func waitPort(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("port %s never came up", addr)
}

// TestDataPathWithOfficialCore exercises the full M3 data path:
// entry listeners -> official naive core -> official naive server -> internet.
// The chain uses a plain h1 proxy hop, so no certificate trust is needed.
func TestDataPathWithOfficialCore(t *testing.T) {
	naive := findNaiveExe(t)
	if testing.Short() {
		t.Skip("short mode")
	}

	// 1. official naive server (h1 CONNECT proxy, user:pass auth).
	srv := exec.Command(naive, "--listen=http://user:pass@127.0.0.1:18095", "--log")
	srv.Stdout = io.Discard
	srv.Stderr = io.Discard
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })
	waitPort(t, "127.0.0.1:18095", 10*time.Second)

	// 2. core manager drives the official client core.
	store := &config.Store{CorePath: naive, InternalSocks: "127.0.0.1:41095"}
	plainTLS := false // plain h1 hop: no certificate trust needed locally
	profile := &config.Profile{
		Name:       "e2e",
		Server:     "127.0.0.1",
		Port:       18095,
		Username:   "user",
		Password:   "pass",
		TLS:        &plainTLS,
		LocalSocks: "127.0.0.1:11095",
		LocalHTTP:  "127.0.0.1:18085",
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := coremgr.NewManager()
	if err := mgr.Start(ctx, store, profile); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)

	// 3. entry listeners forward into the core.
	st := &stats.Stats{}
	em := NewManager(st)
	if err := em.Start(profile.LocalSocks, profile.LocalHTTP, store.InternalSocks); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(em.Stop)
	waitPort(t, profile.LocalSocks, 10*time.Second)
	// The core (official naive.exe) takes a moment to open its socks port.
	waitPort(t, store.InternalSocks, 15*time.Second)

	fetch := func(t *testing.T, tr *http.Transport) {
		t.Helper()
		client := &http.Client{Transport: tr, Timeout: 30 * time.Second}
		resp, err := client.Get("https://api.github.com/zen")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil || len(body) == 0 {
			t.Fatalf("body: %v (%d bytes)", err, len(body))
		}
	}

	t.Run("socks5", func(t *testing.T) {
		d, err := proxy.SOCKS5("tcp", profile.LocalSocks, nil, proxy.Direct)
		if err != nil {
			t.Fatal(err)
		}
		ctxDialer, ok := d.(proxy.ContextDialer)
		if !ok {
			t.Fatal("socks dialer lacks context support")
		}
		tr := &http.Transport{DialContext: ctxDialer.DialContext}
		fetch(t, tr)
	})
	t.Run("http", func(t *testing.T) {
		pu, err := url.Parse("http://" + profile.LocalHTTP)
		if err != nil {
			t.Fatal(err)
		}
		tr := &http.Transport{Proxy: http.ProxyURL(pu)}
		fetch(t, tr)
	})
	if st.UpBytes.Load() == 0 || st.DownBytes.Load() == 0 {
		t.Errorf("stats not counting: up=%d down=%d", st.UpBytes.Load(), st.DownBytes.Load())
	}
}
