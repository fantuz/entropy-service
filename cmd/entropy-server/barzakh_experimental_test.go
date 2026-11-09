//go:build experimental_barzakh

package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestExperimentalBarzakhWebSocketAndRelease(t *testing.T) {
	parent := newBenchmarkDRBG(t, drbg.Barzakh521)
	reference := newBenchmarkDRBG(t, drbg.Barzakh521)
	baseline := drbg.ActiveInstances()

	reader := drbg.NewConnectionReader(reference)
	defer reader.Close()

	budget := newWebSocketBudget(4, 4<<20)
	done := make(chan struct{})
	handler := wsBinaryHandler(parent, 1, 65, 256, budget)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)

		handler(w, r)
	}))
	defer server.Close()

	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}

	conn, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/stream?bytes=65&refresh=1", nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		kind, got, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatal(readErr)
		}

		want := make([]byte, 65)
		if _, err := io.ReadFull(reader, want); err != nil {
			t.Fatal(err)
		}

		if kind != websocket.BinaryMessage || !bytes.Equal(got, want) {
			t.Fatal("WebSocket did not use a continuous private Barzakh stream")
		}
	}

	_ = conn.Close()
	reader.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("closed WebSocket did not release its handler")
	}

	if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
		t.Fatalf("experimental WebSocket leaked admission: %+v", usage)
	}

	if drbg.ActiveInstances() != baseline {
		t.Fatal("experimental WebSocket leaked a private generator")
	}
}
