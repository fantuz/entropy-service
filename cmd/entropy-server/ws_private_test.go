package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestWebSocketsUsePrivateGenerators(t *testing.T) {
	for _, algorithm := range []drbg.Algorithm{drbg.ChaCha20, drbg.AES256CTR} {
		for _, path := range []string{"/stream", "/bytes", "/colors", "/words"} {
			t.Run(algorithm.String()+path, func(t *testing.T) {
				seed := bytes.Repeat([]byte{0x5a}, 64)

				parent, err := drbg.NewDRBGWithAlgorithm(seed, algorithm)
				if err != nil {
					t.Fatal(err)
				}

				reference, err := drbg.NewDRBGWithAlgorithm(seed, algorithm)
				if err != nil {
					t.Fatal(err)
				}

				handler := wsBytesHandler(&parent, 1, 64, newWebSocketBudget(128, 1<<30))

				switch path {
				case "/stream":
					handler = wsBinaryHandler(&parent, 1, 64, 64, newWebSocketBudget(128, 1<<30))
				case "/words":
					handler = wsWordsHandler(&parent, 3, 1, newWebSocketBudget(128, 1<<30))
				}

				done := make(chan struct{}, 2)

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer func() { done <- struct{}{} }()

					handler(w, r)
				}))
				defer server.Close()

				for range 2 {
					conn, response, err := websocket.DefaultDialer.DialContext(t.Context(),
						"ws"+strings.TrimPrefix(server.URL, "http")+path, nil)
					if response != nil && response.Body != nil {
						_ = response.Body.Close()
					}

					if err != nil {
						t.Fatal(err)
					}

					defer conn.Close()

					child, err := drbg.NewConnectionDRBG(&reference)
					if err != nil {
						t.Fatal(err)
					}

					_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

					for range 2 {
						kind, payload, err := conn.ReadMessage()
						if err != nil {
							t.Fatal(err)
						}

						checkPrivateFrame(t, path, kind, payload, &child)
					}

					_ = conn.Close()

					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Fatal("disconnect did not release the connection")
					}

					drbg.DecreaseActiveInstances(1) // reference child
				}
			})
		}
	}
}

func checkPrivateFrame(t *testing.T, path string, kind int, payload []byte, child *drbg.DRBG) {
	t.Helper()

	if path == "/words" {
		want, err := newWordFrame(child, 3)
		if err != nil {
			t.Fatal(err)
		}

		var frame EntropyFrame
		if err := json.Unmarshal(payload, &frame); err != nil || kind != websocket.TextMessage || frame != want {
			t.Fatalf("word frame did not use private %s stream: %v", child.Algorithm(), err)
		}

		return
	}

	want := make([]byte, 64)
	if _, err := io.ReadFull(child, want); err != nil {
		t.Fatal(err)
	}

	if path == "/stream" {
		if kind != websocket.BinaryMessage || !bytes.Equal(payload, want) {
			t.Fatal("binary frame did not use its private stream")
		}

		return
	}

	var frame EntropyDataFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatal(err)
	}

	hash := sha256.Sum256(want)
	if kind != websocket.TextMessage || frame.Hex != hex.EncodeToString(want) ||
		frame.Base64 != base64.StdEncoding.EncodeToString(want) || frame.Hash != hex.EncodeToString(hash[:]) {
		t.Fatal("JSON frame content or hash did not match its private stream")
	}
}
