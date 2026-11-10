package main

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
)

var errSampleComplete = errors.New("preflight sample received")

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

	var data []byte

	err := client.ReceiveEntropy(ctx, wsURL, func(frame client.StreamFrame) error {
		data = bytes.Clone(frame.Data)
		frame.Release()

		return errSampleComplete
	})
	if errors.Is(err, errSampleComplete) {
		return data, nil
	}

	return nil, err
}
