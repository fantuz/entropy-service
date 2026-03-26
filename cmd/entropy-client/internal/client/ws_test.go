package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStreamEntropyWS(t *testing.T) {

	upgrader := websocket.Upgrader{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal(err)
		}

		defer conn.Close()

		data := make([]byte, 512)

		for i := 0; i < 5; i++ {
			conn.WriteMessage(websocket.BinaryMessage, data)
		}
	}))
	defer srv.Close()

	//wsURL := "ws://127.0.0.1:8080/stream?bytes=65536"
	wsURL := "ws" + srv.URL[4:]

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := StreamEntropy(ctx, wsURL) // 2097152
	if err != nil {
		t.Fatal(err)
	}

	total := 0

	for msg := range ch {
		total += len(msg)
		if total >= 5*512 {
			break
		}
	}

	if total == 0 {
		t.Fatal("no websocket data received")
	}
}
