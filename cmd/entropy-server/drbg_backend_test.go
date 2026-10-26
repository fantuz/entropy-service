package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestRandomBytesInheritsBackend(t *testing.T) {
	for _, algorithm := range []drbg.Algorithm{drbg.ChaCha20, drbg.AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			seed := bytes.Repeat([]byte{0x5a}, 64)

			parent, err := drbg.NewDRBGWithAlgorithm(seed, algorithm)
			if err != nil {
				t.Fatal(err)
			}

			referenceParent, err := drbg.NewDRBGWithAlgorithm(seed, algorithm)
			if err != nil {
				t.Fatal(err)
			}

			childSeed, err := referenceParent.Derive(64)
			if err != nil {
				t.Fatal(err)
			}

			child, err := drbg.NewDRBGWithAlgorithm(childSeed, algorithm)
			if err != nil {
				t.Fatal(err)
			}

			want := make([]byte, 64)
			if _, err := io.ReadFull(&child, want); err != nil {
				t.Fatal(err)
			}

			w := httptest.NewRecorder()
			randomBytesHandler(&parent, 64, 20, false)(w,
				httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/data/random?bytes=64", nil))

			if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), want) {
				t.Fatalf("handler did not use the selected backend: status %d", w.Code)
			}

			if w.Header().Get("X-Rng-Drbg") != algorithm.String() {
				t.Fatal("response header does not match the selected backend")
			}
		})
	}
}
