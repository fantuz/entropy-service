package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWriteout struct {
	bytes.Buffer

	write    func(context.Context, []byte) (int, error)
	closeErr error
	closes   int
}

func (s *fakeWriteout) WriteContext(ctx context.Context, p []byte) (int, error) {
	if s.write != nil {
		return s.write(ctx, p)
	}

	return s.Write(p)
}
func (s *fakeWriteout) Close() error { s.closes++; return s.closeErr }

func TestWriteoutAllFramesOnBothTransports(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "writeout.jsonl")
			cfg := experimentConfig(mode, serveExperiment(t, mode), path)
			cfg.WriteOut = true
			cfg.AnalysisInterval = time.Hour
			cfg.Duration = 5 * time.Second
			cfg.OutputBytes = 3 * cfg.Slice // Three complete frames, independent of dial/scheduler speed.
			sink := &fakeWriteout{}
			opens := 0

			if err := runConfiguredWithWriteout(t.Context(), cfg, func() (writeoutSink, error) { opens++; return sink, nil }); err != nil {
				t.Fatal(err)
			}

			records := readReport(t, path)

			summary := decodeRecord[summaryRecord](t, records[len(records)-1])
			if opens != 1 || sink.closes != 1 || summary.DeviceErrors != 0 || summary.DeviceFrames != summary.ReceivedFrames || int64(summary.DeviceBytes) != summary.ReceivedBytes || sink.Len() != int(summary.DeviceBytes) || summary.AnalysedSamples != 1 || summary.ReceivedFrames != 3 || summary.Termination != reasonOutputLimit {
				t.Fatalf("incomplete writeout accounting: %+v opens=%d closes=%d", summary, opens, sink.closes)
			}

			for i, b := range sink.Bytes() {
				if b != byte(i%256) {
					t.Fatal("writeout changed received bytes or wrote only analysed prefixes")
				}
			}
		})
	}
}

func TestWriteoutOpenFailureStopsBeforeNetwork(t *testing.T) {
	var requests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "failure.jsonl")
	cfg := experimentConfig(modePull, server.URL, path)
	cfg.WriteOut = true
	err := runConfiguredWithWriteout(t.Context(), cfg, func() (writeoutSink, error) { return nil, os.ErrPermission })
	records := readReport(t, path)

	summary := decodeRecord[summaryRecord](t, records[len(records)-1])
	if !errors.Is(err, os.ErrPermission) || requests.Load() != 0 || len(records) != 2 || summary.Termination != reasonStartupError || !summary.Failed || summary.MeasurementBegan || summary.DeviceErrors != 1 {
		t.Fatalf("open failure was not fatal before transport: %+v err=%v", summary, err)
	}
}

func TestWriteoutFailuresAndPartialCounts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		short    bool
		closeErr error
	}{
		{"partial-write", true, nil}, {"close", false, io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "failure.jsonl")
			cfg := experimentConfig(modePull, serveExperiment(t, modePull), path)
			cfg.WriteOut = true

			sink := &fakeWriteout{closeErr: tc.closeErr}
			if tc.short {
				// The injected error ends the run. A narrow duration can expire
				// before the first dial/write on a busy Windows runner.
				cfg.Duration = 5 * time.Second
				sink.write = func(context.Context, []byte) (int, error) { return 7, io.ErrShortWrite }
			}

			err := runConfiguredWithWriteout(t.Context(), cfg, func() (writeoutSink, error) { return sink, nil })
			records := readReport(t, path)

			summary := decodeRecord[summaryRecord](t, records[len(records)-1])
			if err == nil || !summary.Failed || summary.Termination != reasonWriteoutError || summary.DeviceErrors != 1 || sink.closes != 1 {
				t.Fatalf("writeout error was hidden: %+v err=%v closes=%d", summary, err, sink.closes)
			}

			if tc.short && (summary.DeviceBytes != 7 || summary.DeviceFrames != 0 || summary.ReceivedFrames != 1 || summary.AnalysedSamples != 1 || len(records) != 4) {
				t.Fatalf("partial failure accounting: %+v", summary)
			}
		})
	}
}

func TestWriteoutCancellationAndConcurrentError(t *testing.T) {
	for _, realError := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "cancel.jsonl")
		cfg := experimentConfig(modePull, serveExperiment(t, modePull), path)
		cfg.WriteOut = true
		cfg.Duration = 5 * time.Second // The first writer invocation cancels the run.
		ctx, cancel := context.WithCancel(t.Context())
		sink := &fakeWriteout{write: func(context.Context, []byte) (int, error) {
			cancel()

			if realError {
				return 7, io.ErrShortWrite
			}

			return 7, context.Canceled
		}}
		err := runConfiguredWithWriteout(ctx, cfg, func() (writeoutSink, error) { return sink, nil })

		cancel()

		records := readReport(t, path)

		summary := decodeRecord[summaryRecord](t, records[len(records)-1])
		if summary.DeviceBytes != 7 || summary.DeviceFrames != 0 || sink.closes != 1 {
			t.Fatalf("canceled write lost cleanup/progress: %+v", summary)
		}

		if realError {
			if !errors.Is(err, io.ErrShortWrite) || !summary.Failed || summary.DeviceErrors != 1 {
				t.Fatal("cancellation hid a real device error")
			}
		} else if err != nil || summary.Failed || summary.DeviceErrors != 0 || summary.Termination != reasonInterrupted {
			t.Fatalf("ordinary cancellation became a device failure: %+v err=%v", summary, err)
		}
	}
}

func TestWriteoutDisabledNeverOpensDevice(t *testing.T) {
	cfg := experimentConfig(modePull, serveExperiment(t, modePull), "")
	if err := runConfiguredWithWriteout(t.Context(), cfg, func() (writeoutSink, error) { t.Fatal("default run opened the device"); return nil, os.ErrPermission }); err != nil {
		t.Fatal(err)
	}
}

func TestWriteoutPreflightDoesNotMixStartupSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preflight.jsonl")
	cfg := experimentConfig(modePull, serveExperiment(t, modePull), path)
	cfg.WriteOut, cfg.Headless = true, false

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	sink := &fakeWriteout{}

	if err := runConfiguredWithWriteout(ctx, cfg, func() (writeoutSink, error) { return sink, nil }); err != nil {
		t.Fatal(err)
	}

	records := readReport(t, path)

	summary := decodeRecord[summaryRecord](t, records[len(records)-1])
	if sink.Len() != 0 || sink.closes != 1 || summary.DeviceBytes != 0 || summary.MeasurementBegan || summary.Termination != reasonInterrupted {
		t.Fatalf("startup sample was mixed or sink was not closed: %+v", summary)
	}
}
