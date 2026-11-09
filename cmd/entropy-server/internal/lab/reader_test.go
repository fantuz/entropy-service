package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testRegistry(t *testing.T, config Config) *Registry {
	t.Helper()

	registry, err := New([]Config{config})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(registry.Close)

	return registry
}

func TestRegistryValidationAndPublicMetadata(t *testing.T) {
	for _, config := range []Config{
		{ID: "../bad", Kind: "tcp", Endpoint: "127.0.0.1:9000"},
		{ID: "bad", Kind: "shell", Endpoint: "cat /dev/urandom"},
		{ID: "bad", Kind: "http", Endpoint: "http://u:p@example.org"}, //nolint:gosec // Userinfo rejection fixture, never dialled.
		{ID: "bad", Kind: "http", Endpoint: "file:///tmp/sample"},
		{ID: "bad", Kind: "http", Endpoint: "http://localhost/lab/stream"},
		{ID: "bad", Kind: "entropy-ws", Endpoint: "http://localhost/stream"},
		{ID: "bad", Kind: "tcp", Endpoint: "localhost:0"},
		{ID: "bad", Kind: "device", Endpoint: "relative"},
		{ID: "bad", Kind: "http", Endpoint: "http://localhost", MaxBytes: MaxBytes + 1},
		{ID: "bad", Kind: "http", Endpoint: "http://localhost", TimeoutMs: 99},
	} {
		if _, err := New([]Config{config}); err == nil {
			t.Fatalf("accepted invalid source: %+v", config)
		}
	}

	config := Config{ID: "peer", Kind: "http", Endpoint: "http://localhost/private?secret=hidden"}
	if _, err := New([]Config{config, config}); err == nil {
		t.Fatal("duplicate source ID accepted")
	}

	registry := testRegistry(t, config)

	descriptor, ok := registry.Lookup("peer")
	if !ok || descriptor.MaxBytes != MaxBytes || descriptor.TimeoutMs != 5000 {
		t.Fatal("incorrect registry defaults")
	}

	public, err := json.Marshal(registry.List())
	if err != nil || strings.Contains(string(public), "secret") || strings.Contains(string(public), "endpoint") {
		t.Fatalf("endpoint leaked into public metadata: %s", public)
	}

	for _, body := range []string{`{"sources":[],"unknown":true}`, `{"sources":[]} {}`, strings.Repeat(" ", 65537)} {
		path := filepath.Join(t.TempDir(), "lab.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		if _, err := Load(path); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestHTTPBoundsRedirectsAndCancellation(t *testing.T) {
	var redirected atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small":
			_, _ = w.Write([]byte{1, 2, 3})
		case "/large":
			_, _ = w.Write(make([]byte, 33))
		case "/redirect":
			http.Redirect(w, r, "/target", http.StatusFound)
		case "/target":
			redirected.Add(1)
		case "/stall":
			<-r.Context().Done()
		}
	}))
	defer server.Close()

	for _, path := range []string{"small", "large", "redirect", "stall"} {
		t.Run(path, func(t *testing.T) {
			registry := testRegistry(t, Config{ID: "source", Kind: "http", Endpoint: server.URL + "/" + path, TimeoutMs: 100})

			stream, err := registry.Open(t.Context(), "source", 32, 20)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			started := time.Now()

			data, err := stream.Read(t.Context(), 32)
			if path == "small" {
				if err != nil || !bytes.Equal(data, []byte{1, 2, 3}) {
					t.Fatalf("wrong body: %x %v", data, err)
				}
			} else if err == nil {
				t.Fatal("expected bounded read error")
			}

			if path == "large" && !errors.Is(err, ErrTooLarge) {
				t.Fatal(err)
			}

			if time.Since(started) > time.Second {
				t.Fatal("source read exceeded timeout")
			}
		})
	}

	if redirected.Load() != 0 {
		t.Fatal("HTTP source followed an unregistered redirect")
	}
}

func TestWebSocketBinaryBoundsAndPeerParameters(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" || r.URL.Query().Get("bytes") != "32" || r.URL.Query().Get("refresh") != "20" {
			t.Error("missing entropy peer parameters")
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteMessage(websocket.BinaryMessage, bytes.Repeat([]byte{7}, 32))
		_ = conn.WriteMessage(websocket.BinaryMessage, make([]byte, 33))
	}))
	defer server.Close()

	registry := testRegistry(t, Config{ID: "peer", Kind: "entropy-ws", Endpoint: server.URL})

	stream, err := registry.Open(t.Context(), "peer", 32, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	data, err := stream.Read(t.Context(), 32)
	if err != nil || len(data) != 32 || data[0] != 7 {
		t.Fatalf("wrong upstream data: %v", err)
	}

	if _, err := stream.Read(t.Context(), 32); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized WebSocket message must report ErrTooLarge, got %v", err)
	}
}

func TestWebSocketNormalCloseIsEOF(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
	}))
	defer server.Close()

	registry := testRegistry(t, Config{ID: "finite", Kind: "websocket", Endpoint: "ws" + strings.TrimPrefix(server.URL, "http")})

	stream, err := registry.Open(t.Context(), "finite", 32, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	if _, err := stream.Read(t.Context(), 32); !errors.Is(err, io.EOF) {
		t.Fatalf("normal upstream close must report EOF, got %v", err)
	}
}

func TestTCPAndUnixEOF(t *testing.T) {
	for _, kind := range []string{"tcp", "unix"} {
		address := "127.0.0.1:0"
		if kind == "unix" {
			address = filepath.Join(t.TempDir(), "sample.sock")
		}

		listener, err := (&net.ListenConfig{}).Listen(t.Context(), kind, address)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)

			conn, err := listener.Accept()
			if err == nil {
				_, _ = conn.Write([]byte("fixture"))
				_ = conn.Close()
			}
		}()

		registry := testRegistry(t, Config{ID: "socket", Kind: kind, Endpoint: listener.Addr().String()})

		stream, err := registry.Open(t.Context(), "socket", 32, 20)
		if err != nil {
			t.Fatal(err)
		}

		data, err := stream.Read(t.Context(), 32)
		_ = stream.Close()

		if !errors.Is(err, io.EOF) || string(data) != "fixture" {
			t.Fatalf("lost partial EOF: %q %v", data, err)
		}

		<-done
	}
}

func TestUDPDatagramsAndParentCancellation(t *testing.T) {
	reserve, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	address := reserve.LocalAddr().String()
	_ = reserve.Close()
	registry := testRegistry(t, Config{ID: "udp", Kind: "udp", Endpoint: address})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stream, err := registry.Open(ctx, "udp", 8, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	sender, err := (&net.Dialer{}).DialContext(t.Context(), "udp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	_, _ = sender.Write([]byte("packet"))

	data, err := stream.Read(ctx, 8)
	if err != nil || string(data) != "packet" {
		t.Fatalf("wrong datagram: %q %v", data, err)
	}

	_, _ = sender.Write(make([]byte, 9))

	if _, err := stream.Read(ctx, 8); !errors.Is(err, ErrTooLarge) {
		t.Fatal("oversized datagram was truncated silently")
	}

	done := make(chan error, 1)

	go func() { _, err := stream.Read(ctx, 8); done <- err }()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock UDP")
	}
}
