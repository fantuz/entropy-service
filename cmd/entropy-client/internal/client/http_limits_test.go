package client

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

type sizeTestTransport struct {
	length int64
	body   *sizeTestBody
}

func (s sizeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, ContentLength: s.length,
		Header: make(http.Header), Body: s.body, Request: r,
	}, nil
}

type sizeTestBody struct {
	remaining, read int64
	closed          bool
}

func (b *sizeTestBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}

	n := int(min(int64(len(p)), b.remaining))
	clear(p[:n])
	b.remaining -= int64(n)
	b.read += int64(n)

	return n, nil
}

func (b *sizeTestBody) Close() error {
	b.closed = true

	return nil
}

func TestFetchEntropyResponseLimit(t *testing.T) {
	original := entropyHTTPClient

	t.Cleanup(func() { entropyHTTPClient = original })

	for _, tc := range []struct {
		name                       string
		length, bodySize, wantRead int64
		requested                  int64
		wantError                  bool
	}{
		{"small unknown length", -1, 64, 64, 64, false},
		{"exact maximum", maxEntropyBytes, maxEntropyBytes, maxEntropyBytes, 64, false},
		{"declared oversized", maxEntropyBytes + 1, maxEntropyBytes + 1, 0, 64, true},
		{"unknown oversized", -1, maxEntropyBytes + 4096, maxEntropyBytes + 1, 64, true},
		{"known length with excess data", maxEntropyBytes, maxEntropyBytes + 4096, maxEntropyBytes + 1, maxEntropyBytes, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &sizeTestBody{remaining: tc.bodySize}
			entropyHTTPClient = &http.Client{Transport: sizeTestTransport{length: tc.length, body: body}}
			before := diag.ClientRateMeter.Total()

			data, err := FetchEntropy(t.Context(), "http://entropy.test/", tc.requested)
			if tc.wantError {
				if !errors.Is(err, ErrEntropyTooLarge) || data != nil || diag.ClientRateMeter.Total() != before {
					t.Fatalf("oversized response accepted or counted: len=%d err=%v", len(data), err)
				}
			} else if err != nil || int64(len(data)) != tc.bodySize {
				t.Fatalf("legal response rejected: len=%d err=%v", len(data), err)
			}

			if body.read != tc.wantRead || !body.closed {
				t.Fatalf("unbounded read or unclosed body: %+v", body)
			}
		})
	}
}

func TestFetchEntropyFixedLengthBodies(t *testing.T) {
	const size = 128 << 10

	pattern := []byte("prefix and remainder")
	payload := bytes.Repeat(pattern, size/len(pattern)+1)[:size]

	for _, tc := range []struct {
		name                      string
		declared, sent, requested int
	}{
		{"large complete", size, size, size},
		{"shorter valid response", size, size, size * 2},
		{"truncated near start", maxEntropyBytes, 1, maxEntropyBytes},
		{"truncated after prefix", size, size - 1, size},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(tc.declared))
				_, _ = w.Write(payload[:tc.sent])
			}))
			defer srv.Close()

			before := diag.ClientRateMeter.Total()

			data, err := FetchEntropy(t.Context(), srv.URL, int64(tc.requested))
			if tc.sent < tc.declared {
				if !errors.Is(err, io.ErrUnexpectedEOF) || data != nil || before != diag.ClientRateMeter.Total() {
					t.Fatalf("incomplete body returned %d bytes, error %v", len(data), err)
				}

				return
			}

			if err != nil || !bytes.Equal(data, payload[:tc.sent]) {
				t.Fatalf("expected complete response, got %d bytes, error %v", len(data), err)
			}
		})
	}
}

// Benchmark the client's allocations with declared lengths, independent of network latency.
func BenchmarkFetchEntropySized(b *testing.B) {
	original := entropyHTTPClient

	b.Cleanup(func() { entropyHTTPClient = original })

	for _, size := range []int64{8 << 20, maxEntropyBytes} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			body := &sizeTestBody{}
			entropyHTTPClient = &http.Client{Transport: sizeTestTransport{length: size, body: body}}
			b.SetBytes(size)
			b.ReportAllocs()

			for b.Loop() {
				*body = sizeTestBody{remaining: size}

				data, err := FetchEntropy(b.Context(), "http://entropy.test/", size)
				if err != nil || int64(len(data)) != size {
					b.Fatalf("fetch returned %d bytes: %v", len(data), err)
				}
			}
		})
	}
}
