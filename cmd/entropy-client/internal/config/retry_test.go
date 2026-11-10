package config

import (
	"testing"
	"time"
)

func TestRetryValidation(t *testing.T) {
	for _, tc := range []struct {
		retries int
		delay   time.Duration
		valid   bool
	}{{0, 0, true}, {2, time.Second, true}, {-1, time.Second, false}, {101, time.Second, false}, {1, 0, false}, {1, -time.Second, false}, {1, 31 * time.Second, false}} {
		cfg := &Config{Retries: tc.retries, RetryDelay: tc.delay, DisplayInterval: time.Second, ShannonThreshold: 7}
		if err := cfg.ValidateAnalysis(); (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
