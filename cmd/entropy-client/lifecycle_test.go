package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSampleUsesSelectedTransport(t *testing.T) {
	data := []byte{1, 2, 3, 4}

	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			closed := make(chan struct{})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)

				if mode == modePull {
					_, _ = w.Write(data)

					return
				}

				upgrader := websocket.Upgrader{}

				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()

				_ = conn.WriteMessage(websocket.BinaryMessage, data)
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()

			httpURL, wsURL := server.URL, "invalid://unused"
			if mode == modeStream {
				httpURL, wsURL = "invalid://unused", "ws"+strings.TrimPrefix(server.URL, "http")
			}

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			got, err := sampleEntropy(ctx, mode, httpURL, wsURL, 4)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("got %x, %v", got, err)
			}

			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("sample left a connection open")
			}
		})
	}
}

func TestCanceledWait(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan bool, 1)

	go func() { done <- waitContext(ctx, time.Hour) }()

	select {
	case completed := <-done:
		if completed {
			t.Fatal("canceled wait reported completion")
		}
	case <-time.After(time.Second):
		t.Fatal("wait ignored cancellation")
	}
}
