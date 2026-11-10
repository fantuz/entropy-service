package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

type retryBudget struct {
	limit, used int
	delay       time.Duration
}

func (b *retryBudget) next(err error) (time.Duration, bool) {
	hint, retryable := client.RetryableFailure(err)
	if !retryable || b.used >= b.limit || hint > config.MaxRetryDelay {
		return 0, false
	}

	delay := b.delay
	for range b.used {
		delay = min(config.MaxRetryDelay, 2*delay)
	}
	// Jitter avoids synchronized clients retrying an unhealthy root together.
	delay = min(config.MaxRetryDelay, delay+time.Duration(rand.Int64N(int64(delay/4)+1))) //nolint:gosec // Scheduling jitter is not cryptographic output.
	b.used++

	return max(delay, hint), true
}

func receiveFrames(ctx context.Context, cfg *config.Config, httpURL, wsURL string, out chan<- client.StreamFrame, failures chan<- error) {
	defer close(out)

	budget := retryBudget{limit: cfg.Retries, delay: cfg.RetryDelay}
	deliver := func(frame client.StreamFrame) error {
		select {
		case out <- frame:
			return nil
		case <-ctx.Done():
			frame.Release()
			return ctx.Err()
		}
	}

	for {
		var err error
		if cfg.Mode == modeStream {
			err = client.ReceiveEntropy(ctx, wsURL, deliver)
		} else {
			err = pullFramesUntilError(ctx, cfg, httpURL, out)
		}

		if contextEnded(ctx) {
			return
		}

		delay, retry := budget.next(err)
		if !retry {
			failures <- fmt.Errorf("entropy transport stopped after %d retries: %w", budget.used, err)
			return
		}

		log.Printf("transport retry %d/%d in %s: %v", budget.used, budget.limit, delay.Round(time.Millisecond), err)

		if !waitContext(ctx, delay) {
			return
		}
	}
}
