package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

func TestRetryBudgetBoundsAndHints(t *testing.T) {
	budget := retryBudget{limit: 3, delay: time.Millisecond}
	for attempt := range 3 {
		delay, ok := budget.next(&client.HTTPStatusError{Code: 503})

		base := time.Millisecond << attempt
		if !ok || delay < base || delay > base+base/4 {
			t.Fatalf("attempt=%d delay=%v ok=%t", attempt, delay, ok)
		}
	}

	if _, ok := budget.next(&client.HTTPStatusError{Code: 503}); ok {
		t.Fatal("retry budget reset")
	}

	budget = retryBudget{limit: 10, delay: time.Millisecond}
	if delay, ok := budget.next(&client.HTTPStatusError{Code: 503, RetryAfter: 2 * time.Second}); !ok || delay != 2*time.Second {
		t.Fatalf("server hint ignored: %v %t", delay, ok)
	}

	if _, ok := budget.next(&client.HTTPStatusError{Code: 503, RetryAfter: config.MaxRetryDelay + time.Second}); ok {
		t.Fatal("retry before a long server hint")
	}

	if _, ok := budget.next(errors.New("permanent")); ok {
		t.Fatal("retrying unknown failure")
	}

	budget = retryBudget{limit: 100, delay: 20 * time.Second}
	for range 10 {
		if delay, ok := budget.next(&client.HTTPStatusError{Code: 503}); !ok || delay > config.MaxRetryDelay {
			t.Fatalf("unbounded backoff: %v", delay)
		}
	}
}

func TestRetriesPreserveExportLimitAcrossConnections(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			var calls, success atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := calls.Add(1)
				if mode == modePull {
					if attempt <= 2 {
						w.Header().Set("Retry-After", "0")
						http.Error(w, "seed paused", http.StatusServiceUnavailable)

						return
					}

					_, _ = w.Write(bytes.Repeat([]byte{byte(success.Add(1))}, 256))

					return
				}

				upgrader := websocket.Upgrader{}

				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()

				_ = conn.WriteMessage(websocket.BinaryMessage, bytes.Repeat([]byte{byte(success.Add(1))}, 256))
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "seed paused"), time.Now().Add(time.Second))
			}))
			defer server.Close()

			endpoint := server.URL
			if mode == modeStream {
				endpoint = "ws" + strings.TrimPrefix(endpoint, "http")
			}

			dir := t.TempDir()
			cfg := experimentConfig(mode, endpoint, filepath.Join(dir, "report.jsonl"))
			cfg.Retries, cfg.RetryDelay = 2, time.Millisecond
			cfg.Duration = 2 * time.Second

			cfg.OutputPath, cfg.OutputBytes = filepath.Join(dir, "raw.bin"), 600
			if err := runConfigured(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}

			data, err := os.ReadFile(cfg.OutputPath)

			want := append(bytes.Repeat([]byte{1}, 256), bytes.Repeat([]byte{2}, 256)...)

			want = append(want, bytes.Repeat([]byte{3}, 88)...)
			if err != nil || !bytes.Equal(data, want) {
				t.Fatalf("output was reset, duplicated or contaminated: %d %v", len(data), err)
			}

			records := readReport(t, cfg.ReportPath)

			summary := decodeRecord[summaryRecord](t, records[len(records)-1])
			if summary.Failed || summary.Termination != reasonOutputLimit || summary.OutputBytes != 600 || summary.ReceivedFrames != 3 {
				t.Fatalf("%+v", summary)
			}

			wantCalls := int32(3)
			if mode == modePull {
				wantCalls = 5
			}

			if calls.Load() != wantCalls {
				t.Fatalf("attempts=%d want=%d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestRetryDefaultExhaustionAndPermanentFailure(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		retries, status, wantCalls int
	}{{"disabled", 0, 503, 1}, {"exhausted", 2, 503, 3}, {"forbidden", 3, 403, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); http.Error(w, "unavailable", tc.status) }))
			defer server.Close()

			cfg := experimentConfig(modePull, server.URL, "")

			cfg.Retries, cfg.RetryDelay, cfg.Duration = tc.retries, time.Millisecond, time.Second
			if err := runConfigured(t.Context(), cfg); err == nil {
				t.Fatal("transport failure reported success")
			}

			if int(calls.Load()) != tc.wantCalls {
				t.Fatalf("calls=%d want=%d", calls.Load(), tc.wantCalls)
			}
		})
	}
}

func TestRetryWaitRespectsRunDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "paused", http.StatusServiceUnavailable) }))
	defer server.Close()

	cfg := experimentConfig(modePull, server.URL, "")
	cfg.Retries, cfg.RetryDelay, cfg.Duration = 3, time.Second, 40*time.Millisecond
	started := time.Now()

	if err := runConfigured(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("duration did not interrupt retry wait")
	}
}

func TestRetryDiscardsTruncatedHTTPBody(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "256")

		if calls.Add(1) == 1 {
			_, _ = w.Write(bytes.Repeat([]byte{0xee}, 16))
			return
		}

		_, _ = w.Write(bytes.Repeat([]byte{1}, 256))
	}))
	defer server.Close()

	cfg := experimentConfig(modePull, server.URL, "")
	cfg.Retries, cfg.RetryDelay, cfg.Duration = 1, time.Millisecond, time.Second

	cfg.OutputPath, cfg.OutputBytes = filepath.Join(t.TempDir(), "raw.bin"), 256
	if err := runConfigured(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(cfg.OutputPath)
	if err != nil || !bytes.Equal(data, bytes.Repeat([]byte{1}, 256)) || calls.Load() != 2 {
		t.Fatalf("partial HTTP body reached export: len=%d calls=%d error=%v", len(data), calls.Load(), err)
	}
}

func TestMeasuredDisconnectRetainsWebSocketReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "seed stale"), time.Now().Add(time.Second))
	}))
	defer server.Close()

	cfg := experimentConfig(modeStream, "ws"+strings.TrimPrefix(server.URL, "http"), "")

	err := runConfigured(t.Context(), cfg)
	if err == nil || !strings.Contains(err.Error(), "1013") || !strings.Contains(err.Error(), "seed stale") {
		t.Fatalf("lost disconnect reason: %v", err)
	}
}
