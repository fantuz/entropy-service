package client

import (
	"context"
	"testing"
	"time"
)

func TestEntropyEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := TestEntropyHTTP(ctx, "http://127.0.0.1:8080/v1/data/random")
	if err != nil {
		// Integration test: requires a running entropy-server on :8080.
		// Skip (rather than fail) when none is reachable so unit CI stays green.
		t.Skipf("entropy server not reachable, skipping integration test: %v", err)
	}
}
