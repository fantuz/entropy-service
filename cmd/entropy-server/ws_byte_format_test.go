package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestByteFrameFormats(t *testing.T) {
	data := bytes.Repeat([]byte{0, 1, 0x7f, 0x80, 0xff}, 107)
	digest := sha256.Sum256(data)

	for _, format := range []byteFrameFormat{byteFrameJSON, byteFrameCompact, byteFrameHash} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer

			if err := encodeByteFrame(&output, data, format); err != nil {
				t.Fatal(err)
			}

			checkByteFormat(t, format, output.Bytes(), data, digest)
		})
	}
}

func checkByteFormat(t *testing.T, format byteFrameFormat, payload, data []byte, digest [32]byte) {
	t.Helper()

	if format == byteFrameCompact {
		if len(payload) != sha256.Size+len(data) || !bytes.Equal(payload[:sha256.Size], digest[:]) ||
			!bytes.Equal(payload[sha256.Size:], data) {
			t.Fatal("compact message did not contain the full digest followed by the raw sample")
		}

		return
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}

	if format == byteFrameHash {
		var frame struct {
			Hash  string `json:"hash"`
			Bytes int    `json:"bytes"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil || len(fields) != 2 ||
			frame.Hash != hex.EncodeToString(digest[:]) || frame.Bytes != len(data) {
			t.Fatal("hash response contains raw encodings or incorrect sample metadata")
		}

		return
	}

	var frame EntropyDataFrame
	if err := json.Unmarshal(payload, &frame); err != nil || len(fields) != 3 ||
		frame.Hex != hex.EncodeToString(data) || frame.Base64 != base64.StdEncoding.EncodeToString(data) ||
		frame.Hash != hex.EncodeToString(digest[:]) {
		t.Fatal("legacy JSON schema or content changed")
	}
}

func TestByteFrameFormatValidation(t *testing.T) {
	for _, query := range []string{"format=", "format=raw", "format=compact&format=json", "format=hash&format=hash"} {
		values, err := url.ParseQuery(query)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := parseByteFrameFormat(values); err == nil {
			t.Fatalf("accepted ambiguous or invalid format %q", query)
		}

		budget := newWebSocketBudget(1, 1<<20)
		w := httptest.NewRecorder()
		wsBytesHandler(observableTestDRBG(t), 1, 64, budget)(w,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bytes?"+query, nil))

		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "format") || budget.usage().Active != 0 {
			t.Fatal("invalid format reached admission or WebSocket upgrade")
		}
	}
}

func TestByteFormatsUsePrivateGeneratorsAndReleaseBudget(t *testing.T) {
	for _, algorithm := range []drbg.Algorithm{drbg.ChaCha20, drbg.AES256CTR} {
		for _, format := range []byteFrameFormat{byteFrameCompact, byteFrameHash} {
			t.Run(algorithm.String()+"/"+string(format), func(t *testing.T) {
				seed := bytes.Repeat([]byte{0x5a}, 64)

				parent, err := drbg.NewDRBGWithAlgorithm(seed, algorithm)
				if err != nil {
					t.Fatal(err)
				}

				defer func() { _ = parent.Close() }()

				reference, err := drbg.NewDRBGWithAlgorithm(seed, algorithm)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = reference.Close() }()

				budget := newWebSocketBudget(4, 1<<20)

				server := httptest.NewServer(wsBytesHandler(&parent, 1, 4096, budget))
				defer server.Close()
				defer waitBudgetActive(t, budget, 0)

				for _, path := range []string{"/bytes", "/colors"} {
					conn := budgetConnect(t, server, path+"?bytes=4096&format="+string(format))
					defer conn.Close()

					_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

					child, err := drbg.NewConnectionDRBG(&reference)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = child.Close() }()

					for range 2 {
						kind, payload, err := conn.ReadMessage()
						if err != nil {
							t.Fatal(err)
						}

						if (format == byteFrameCompact && kind != websocket.BinaryMessage) ||
							(format == byteFrameHash && kind != websocket.TextMessage) {
							t.Fatal("incorrect WebSocket message type")
						}

						want := make([]byte, 4096)
						if _, err := io.ReadFull(&child, want); err != nil {
							t.Fatal(err)
						}

						checkByteFormat(t, format, payload, want, sha256.Sum256(want))
					}

					if budget.usage().Reserved != wsByteReservation(4096, false) {
						t.Fatal("non-JSON mode retained the JSON memory multiplier")
					}

					_ = conn.Close()

					waitBudgetActive(t, budget, 0)
				}
			})
		}
	}
}

func TestByteFormatMemoryAdmission(t *testing.T) {
	for _, secure := range []bool{false, true} {
		budget := newWebSocketBudget(4, 2<<20)
		server := budgetTestServer(t, budget, secure)

		// One MiB of JSON expansion exceeds this budget; a compact or hash
		// response keeps only the raw sample and fixed connection overhead.
		budgetReject(t, server, "/bytes?bytes=1048576", http.StatusRequestEntityTooLarge)
		budgetReject(t, server, "/colors?bytes=1048576&format=json", http.StatusRequestEntityTooLarge)

		for _, path := range []string{"/bytes?bytes=1048576&format=compact", "/colors?bytes=1048576&format=hash"} {
			conn := budgetConnect(t, server, path)
			if usage := budget.usage(); usage.Reserved != wsByteReservation(1<<20, false) {
				t.Fatalf("incorrect reservation: %+v", usage)
			}

			_ = conn.Close()

			waitBudgetActive(t, budget, 0)
		}
	}
}

func BenchmarkByteFrameFormats(b *testing.B) {
	for _, size := range []int{512 << 10, 8 << 20} {
		for _, format := range []byteFrameFormat{byteFrameJSON, byteFrameCompact, byteFrameHash} {
			b.Run(fmt.Sprintf("%dKiB/%s", size>>10, format), func(b *testing.B) {
				data := bytes.Repeat([]byte{0x5a}, size)
				b.SetBytes(int64(size))
				b.ReportAllocs()

				for b.Loop() {
					if err := encodeByteFrame(io.Discard, data, format); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
