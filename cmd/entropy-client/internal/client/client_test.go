package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

func TestFetchEntropyHTTP(t *testing.T) {
	// fake entropy server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]byte, 65536)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	ctx := context.Background()

	// Bytes resets with the rate window; use the lifetime byte count.
	before := diag.ClientRateMeter.Total()
	// fmt.Println("before: ", diag.ClientRateMeter.Update(len(msg)))
	// data, err := FetchEntropy(ctx, srv.URL, 65536)
	data, err := FetchEntropy(ctx, srv.URL, 65536)
	if err != nil {
		t.Fatal(err)
	}

	if len(data) != 65536 {
		t.Fatalf("expected 65536 bytes, got %d", len(data))
	}

	// diag.ClientRateMeter.Update(len(data))
	after := diag.ClientRateMeter.Total()

	// fmt.Println("after: ", diag.ClientRateMeter.Update(len(msg)))
	if after-before < uint64(len(data)) {
		t.Fatal("rate meter did not account for fetched bytes")
	}
}
