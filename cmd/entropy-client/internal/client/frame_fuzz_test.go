package client

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func FuzzEntropyFrame(f *testing.F) {
	f.Add([]byte{1, 2, 3}, uint8(3), false)
	f.Add([]byte{}, uint8(1), false)
	f.Add([]byte{1, 2}, uint8(1), false)
	f.Add([]byte("text"), uint8(0), true)
	f.Fuzz(func(t *testing.T, data []byte, requested uint8, text bool) {
		if len(data) > 256 {
			t.Skip()
		}

		size := int(requested) % 129

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}

			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))

			kind := websocket.BinaryMessage
			if text {
				kind = websocket.TextMessage
			}

			_ = conn.WriteMessage(kind, data)
		}))
		defer server.Close()

		conn, err := dialWebSocket(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()

		conn.SetReadLimit(512)
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))

		frame, err := readStreamFrame(conn, make(chan []byte, 1), size)
		defer frame.Release()

		valid := !text && (size == 0 || size == len(data))
		if valid && (err != nil || !bytes.Equal(frame.Data, data)) {
			t.Fatalf("valid frame lost: %v", err)
		}

		if !valid && err == nil {
			t.Fatal("text or wrong-size frame accepted")
		}
	})
}
