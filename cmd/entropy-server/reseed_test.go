package main

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

type reseedStep struct {
	data []byte
	err  error
}

type scriptedReseedSource struct {
	done  <-chan struct{}
	steps chan reseedStep
}

func (s *scriptedReseedSource) Read(p []byte) (int, error) {
	select {
	case step := <-s.steps:
		return copy(p, step.data), step.err
	case <-s.done:
		return 0, context.Canceled
	}
}

func waitForReseedState(t *testing.T, ready func() bool) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for reseed state")
		}

		time.Sleep(time.Millisecond)
	}
}

func TestReseedSourceFailureSkippingAndRecovery(t *testing.T) {
	master := observableTestDRBG(t)
	backup := observableTestDRBG(t)
	ctx, cancel := context.WithCancel(t.Context())
	source := &scriptedReseedSource{done: ctx.Done(), steps: make(chan reseedStep, 8)}
	done := make(chan struct{})
	before := metrics.Reseeds()

	go func() {
		defer close(done)

		reseedLoop(ctx, source, 1, 32, master, backup)
	}()

	defer func() { cancel(); <-done }()

	// Master succeeds, but backup ends after 31 of 32 requested bytes.
	source.steps <- reseedStep{data: bytes.Repeat([]byte{1}, 32)}

	source.steps <- reseedStep{data: bytes.Repeat([]byte{2}, 31)}

	source.steps <- reseedStep{err: io.EOF}

	waitForReseedState(t, func() bool { return backup.GetMetadata().ReseedFailuresTotal == 1 })

	m, b := master.GetMetadata(), backup.GetMetadata()
	if m.ReseedsTotal != 1 || m.ReseedFailuresTotal != 0 || b.ReseedsTotal != 0 || b.LastReseedError != io.ErrUnexpectedEOF.Error() {
		t.Fatal("partial source read must affect only the failed/skipped generators")
	}

	// Failure on the first generator means neither generator was refreshed.
	source.steps <- reseedStep{err: io.EOF}

	waitForReseedState(t, func() bool { return backup.GetMetadata().ReseedFailuresTotal == 2 })

	if master.GetMetadata().ReseedFailuresTotal != 1 {
		t.Fatal("first-reader failure did not mark all pending generators")
	}

	source.steps <- reseedStep{data: bytes.Repeat([]byte{3}, 32)}

	source.steps <- reseedStep{data: bytes.Repeat([]byte{4}, 32)}

	waitForReseedState(t, func() bool { return backup.GetMetadata().ReseedsTotal == 1 })

	m, b = master.GetMetadata(), backup.GetMetadata()
	if m.ReseedsTotal != 2 || m.LastReseedError != "" || b.LastReseedError != "" || metrics.Reseeds()-before != 3 {
		t.Fatal("successful reseeding did not recover both generators")
	}

	cancel()
	<-done

	if master.GetMetadata().ReseedFailuresTotal != 1 || backup.GetMetadata().ReseedFailuresTotal != 2 {
		t.Fatal("normal cancellation must not count as a source failure")
	}
}

func observableTestDRBG(t *testing.T) *drbg.DRBG {
	t.Helper()

	d, err := drbg.NewDRBG(make([]byte, 64))
	if err != nil {
		t.Fatal(err)
	}

	d.SetMetadata("test", "/dev/test", "ignored", 250*time.Millisecond, 2048)
	t.Cleanup(func() { drbg.DecreaseActiveInstances(1) })

	return &d
}
