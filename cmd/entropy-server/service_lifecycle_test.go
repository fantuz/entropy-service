package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
)

func TestServerContextCancellationAndReadiness(t *testing.T) {
	cfg, err := config.ParseArguments([]string{"-http=127.0.0.1:0", "-enable-https=false", "-require-device=false"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- runServer(ctx, cfg, func() { close(ready) }) }()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("server returned before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not report readiness")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("server did not join shutdown after external cancellation")
	}
}
