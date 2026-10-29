package client

import (
	"errors"
	"io"
	"net/http"
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
		wantError                  bool
	}{
		{"small unknown length", -1, 64, 64, false},
		{"exact maximum", maxEntropyBytes, maxEntropyBytes, maxEntropyBytes, false},
		{"declared oversized", maxEntropyBytes + 1, maxEntropyBytes + 1, 0, true},
		{"unknown oversized", -1, maxEntropyBytes + 4096, maxEntropyBytes + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &sizeTestBody{remaining: tc.bodySize}
			entropyHTTPClient = &http.Client{Transport: sizeTestTransport{length: tc.length, body: body}}
			before := diag.ClientRateMeter.Total()

			data, err := FetchEntropy(t.Context(), "http://entropy.test/", 64)
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
