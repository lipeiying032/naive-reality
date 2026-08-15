package entry

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"naivereal/tui/internal/stats"
)

func TestReadSocks5ReplyConsumesAllAddressTypes(t *testing.T) {
	tests := []struct {
		name  string
		reply []byte
	}{
		{"ipv4", []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}},
		{"domain", append([]byte{5, 0, 0, 3, 3}, append([]byte("dns"), 0, 53)...)},
		{"ipv6", append([]byte{5, 0, 0, 4}, append(make([]byte, 16), 0, 80)...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bytes.NewReader(append(tt.reply, 0xaa))
			if err := readSocks5Reply(reader); err != nil {
				t.Fatal(err)
			}
			left, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(left, []byte{0xaa}) {
				t.Fatalf("unconsumed bytes = %x", left)
			}
		})
	}
}

func TestServeSocks5RejectsUnsupportedAuthMethods(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		serveSocks5(server, func(string) (net.Conn, error) {
			return nil, errors.New("dial should not be called")
		}, &stats.Stats{})
		done <- nil
	}()
	if _, err := client.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	var response [2]byte
	if _, err := io.ReadFull(client, response[:]); err != nil {
		t.Fatal(err)
	}
	if response != [2]byte{5, 0xff} {
		t.Fatalf("response = %v", response)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("SOCKS handler did not exit")
	}
}

func TestStartClosesSocksListenerWhenHTTPBindFails(t *testing.T) {
	socksReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socksAddr := socksReservation.Addr().String()
	_ = socksReservation.Close()

	httpReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpReservation.Close()
	m := NewManager(&stats.Stats{})
	if err := m.Start(socksAddr, httpReservation.Addr().String(), "127.0.0.1:1"); err == nil {
		t.Fatal("Start should fail when HTTP address is occupied")
	}
	probe, err := net.Listen("tcp", socksAddr)
	if err != nil {
		t.Fatalf("SOCKS listener leaked after partial startup: %v", err)
	}
	_ = probe.Close()
}
