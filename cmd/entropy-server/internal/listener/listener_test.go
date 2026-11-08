package listener

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestTunedListenerTransfersData(t *testing.T) {
	for _, sendBuffer := range []int{0, 1 << 16} {
		ln, err := NewTunedListener(t.Context(), "127.0.0.1:0", sendBuffer)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		client, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		conn, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}

		if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}

		if _, err := conn.Write([]byte("entropy")); err != nil {
			t.Fatal(err)
		}

		got := make([]byte, 7)
		if _, err := io.ReadFull(client, got); err != nil || string(got) != "entropy" {
			t.Fatalf("accepted connection did not transfer data: %q, %v", got, err)
		}
	}
}

func TestTunedListenerReturnsBindErrors(t *testing.T) {
	first, err := NewTunedListener(t.Context(), "127.0.0.1:0", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	for _, address := range []string{first.Addr().String(), "127.0.0.1:-1"} {
		ln, err := NewTunedListener(t.Context(), address, 0)
		if ln != nil {
			_ = ln.Close()

			t.Fatalf("unexpectedly bound %s", address)
		}

		var networkError *net.OpError
		if !errors.As(err, &networkError) {
			t.Fatalf("bind failure did not preserve its network error: %v", err)
		}
	}
}
