package websocketdial

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCancellationWhileUpgradeIsPending(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(map[bool]string{false: "ws", true: "wss"}[encrypted], func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) })

			peer := httptest.NewUnstartedServer(handler)
			if encrypted {
				peer.StartTLS()
			} else {
				peer.Start()
			}
			defer peer.Close()
			defer peer.CloseClientConnections()

			dialer := *websocket.DefaultDialer
			// Trust the fixture identity, retaining normal hostname/chain checks.
			if encrypted {
				transport, ok := peer.Client().Transport.(*http.Transport)
				if !ok {
					t.Fatal("unexpected fixture transport")
				}

				dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan error, 1)

			go func() {
				conn, response, err := Dial(ctx, dialer, "ws"+strings.TrimPrefix(peer.URL, "http"), nil)
				if response != nil && response.Body != nil {
					_ = response.Body.Close()
				}

				if conn != nil {
					_ = conn.Close()
				}

				done <- err
			}()

			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("upgrade not reached")
			}

			cancel()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("upgrade survived cancellation")
			}

			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("transport remained open")
			}
		})
	}
}

func TestSuccessfulHandshakeTransfersOwnership(t *testing.T) {
	var upgrader websocket.Upgrader

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}

			if err := conn.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer peer.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	conn, response, err := Dial(ctx, *websocket.DefaultDialer, "ws"+strings.TrimPrefix(peer.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	cancel() // The helper must have relinquished the transport after success.

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("live")); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != "live" {
		t.Fatalf("handoff: %q %v", data, err)
	}
}

func TestCancellationDuringCustomTLSHandshake(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	connected := make(chan struct{})
	dialer := *websocket.DefaultDialer
	dialer.NetDialTLSContext = func(context.Context, string, string) (net.Conn, error) { close(connected); return client, nil }
	done := make(chan error, 1)

	go func() {
		conn, response, err := Dial(ctx, dialer, "wss://fixture.invalid/", nil)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		if conn != nil {
			_ = conn.Close()
		}

		done <- err
	}()

	<-connected
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("custom TLS transport survived cancellation")
	}
}
