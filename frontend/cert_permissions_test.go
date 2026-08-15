//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGencertRestrictsPrivateKeyPermissions(t *testing.T) {
	dir := t.TempDir()
	runGencert([]string{"-hosts", "127.0.0.1", "-out", dir})
	for _, name := range []string{"ca-key.pem", "server-key.pem"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, got)
		}
	}
}
