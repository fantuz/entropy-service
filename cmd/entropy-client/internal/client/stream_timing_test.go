package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStreamTimingStaysWithBufferedPayload(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer conn.Close()

		for i := range 12 {
			if err := conn.WriteMessage(websocket.BinaryMessage, []byte{byte(i)}); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	started := time.Now()

	frames, err := StreamEntropy(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}

	// Allow the reader to queue messages before the consumer starts processing.
	time.Sleep(20 * time.Millisecond)

	var totalRead time.Duration

	for i := range 12 {
		select {
		case frame, ok := <-frames:
			if !ok || len(frame.Data) != 1 || frame.Data[0] != byte(i) || frame.ReceiveWait <= 0 {
				t.Fatalf("payload/timing mismatch at frame %d: %+v", i, frame)
			}

			totalRead += frame.ReceiveWait
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for buffered frame")
		}
	}

	if totalRead > time.Since(started) {
		t.Fatal("serialized read durations exceed the entire connection lifetime")
	}

	select {
	case _, open := <-frames:
		if open {
			t.Fatal("unexpected extra frame")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not close after peer shutdown")
	}
}
