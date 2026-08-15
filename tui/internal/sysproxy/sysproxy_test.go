//go:build windows

package sysproxy

import (
	"testing"
)

func TestEnableDisableRestoreRoundtrip(t *testing.T) {
	prev, err := Get()
	if err != nil {
		t.Skipf("registry unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := Restore(prev); err != nil {
			t.Errorf("restore system proxy during cleanup: %v", err)
		}
	})
	if _, err := Enable("127.0.0.1:1080", "localhost;127.*"); err != nil {
		t.Skipf("registry write unavailable: %v", err)
	}
	got, err := Get()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Server != "127.0.0.1:1080" {
		t.Errorf("after enable: %+v", got)
	}
	if err := Restore(prev); err != nil {
		t.Fatal(err)
	}
	got2, err := Get()
	if err != nil {
		t.Fatal(err)
	}
	if got2.Enabled != prev.Enabled || got2.Server != prev.Server {
		t.Errorf("after restore: %+v (want %+v)", got2, prev)
	}
}
