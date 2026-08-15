//go:build !windows

package config

import (
	"os"
	"testing"
)

func TestSaveRestrictsProfilePermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := &Store{Profiles: []Profile{{Name: "secret", Password: "password"}}}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("profiles mode = %o, want 600", got)
	}
}
