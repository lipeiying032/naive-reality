//go:build windows

package coremgr

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"naivereal/tui/internal/config"
)

func TestStartFailureDoesNotLeaveManagerRunning(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", blocked)
	m := NewManager()
	store := &config.Store{CorePath: "missing.exe", InternalSocks: "127.0.0.1:41080"}
	p := &config.Profile{Server: "example.com", Port: 443}
	if err := m.Start(context.Background(), store, p); err == nil {
		t.Fatal("Start should fail when the config directory cannot be created")
	}

	t.Setenv("APPDATA", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx, store, p); err != nil {
		t.Fatalf("manager remained unusable after setup failure: %v", err)
	}
	m.Stop()
}
