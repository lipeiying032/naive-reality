package main

import (
	"net"
	"testing"
)

func TestRelayConnReleasesSlotExactlyOnce(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	c := &relayConn{Conn: a, release: func() { <-slots }}
	_ = c.Close()
	_ = c.Close()
	if got := len(slots); got != 0 {
		t.Fatalf("relay slots still occupied: %d", got)
	}
}
