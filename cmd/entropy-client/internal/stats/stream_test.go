package stats

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"
	"time"
)

func TestRunFromReader(t *testing.T) {
	// synthetic random data
	buf := make([]byte, 1<<16) // 64 KB
	_, _ = rand.Read(buf)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	called := 0

	err := RunFromReader(ctx, bytes.NewReader(buf), 4096, 16*4096, func(r StreamResult) {
		called++
		// expect some entropy > 7.5 for crypto/rand
		if r.Shannon < 7.5 {
			t.Fatalf("expected high entropy, got %.4f", r.Shannon)
		}
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if called == 0 {
		t.Fatalf("callback not invoked")
	}
}
