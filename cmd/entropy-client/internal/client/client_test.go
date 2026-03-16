package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	//"fmt"

	"github.com/fantuz/entropy-service/entropy-client/internal/diag"
)

func TestFetchEntropyHTTP(t *testing.T) {

	// fake entropy server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]byte, 1024)
		w.Write(data)
	}))
	defer srv.Close()

	ctx := context.Background()

	//diag.ClientRateMeter.Update(len(data))
	before := diag.ClientRateMeter.Bytes()
	//fmt.Println("before: ", diag.ClientRateMeter.Update(len(msg)))
	data, err := FetchEntropy(ctx, srv.URL, 1024)
	if err != nil {
		t.Fatal(err)
	}

	if len(data) != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", len(data))
	}

	//diag.ClientRateMeter.Update(len(data))
	after := diag.ClientRateMeter.Bytes()

	//fmt.Println("after: ", diag.ClientRateMeter.Update(len(msg)))
	if after <= before {
		t.Fatal("rate meter did not increase")
	}
}
