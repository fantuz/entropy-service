package main

import (
	"context"
	"io"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
)

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

// Sample only the selected transport; stream mode need not expose an HTTP API.
func sampleEntropy(ctx context.Context, mode, httpURL, wsURL string, quantity int64) ([]byte, error) {
	if mode == modePull {
		return client.FetchEntropy(ctx, httpURL, quantity)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frames, err := client.StreamEntropy(ctx, wsURL)
	if err != nil {
		return nil, err
	}

	defer func() {
		cancel()

		for range frames {
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-frames:
		if !ok {
			return nil, io.EOF
		}

		return frame.Data, nil
	}
}
