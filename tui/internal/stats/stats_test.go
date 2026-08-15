package stats

import (
	"io"
	"net"
	"testing"
)

func TestCountingConnTracksClientDirections(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	st := &Stats{}
	counted := NewCountingConn(client, st, true)

	go func() {
		_, _ = peer.Write([]byte("upload"))
	}()
	buf := make([]byte, len("upload"))
	if _, err := io.ReadFull(counted, buf); err != nil {
		t.Fatal(err)
	}
	if got := st.UpBytes.Load(); got != int64(len(buf)) {
		t.Fatalf("upload bytes = %d", got)
	}

	done := make(chan struct{})
	go func() {
		_, _ = io.ReadFull(peer, make([]byte, len("download")))
		close(done)
	}()
	if _, err := counted.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	<-done
	if got := st.DownBytes.Load(); got != int64(len("download")) {
		t.Fatalf("download bytes = %d", got)
	}
}
