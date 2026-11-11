package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

// Keep exercising real OS-pipe deadlines on Linux. Windows anonymous pipes do
// not support them, so its shared lifecycle tests use a buffered TCP pair.
func deadlineSeedPipe(t *testing.T, ctx context.Context) (seedDeadlineSource, io.WriteCloser) {
	t.Helper()

	if !windowsBuild {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

		return r, w
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	writer, err := (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	reader, err := listener.Accept()
	if err != nil {
		_ = writer.Close()

		t.Fatal(err)
	}

	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })

	return reader, writer
}

func pipeSeedInput(t *testing.T, ctx context.Context, timeout time.Duration) (io.Reader, io.WriteCloser) {
	t.Helper()

	r, w := deadlineSeedPipe(t, ctx)

	return (&seedInput{source: r, reader: r, timeout: timeout}).Reader(ctx), w
}

func TestSeedInputTimeoutDiscardsPartialAndRecovers(t *testing.T) {
	input, writer := pipeSeedInput(t, t.Context(), 30*time.Millisecond)
	if _, err := writer.Write([]byte{1, 2}); err != nil {
		t.Fatal(err)
	}

	buf := bytes.Repeat([]byte{0xff}, 8)
	started := time.Now()

	if _, err := input.Read(buf); err == nil {
		t.Fatal("partial seed did not time out")
	}

	if time.Since(started) > time.Second || !bytes.Equal(buf, make([]byte, 8)) {
		t.Fatal("partial seed retained or timeout unbounded")
	}

	want := bytes.Repeat([]byte{7}, 8)
	if _, err := writer.Write(want); err != nil {
		t.Fatal(err)
	}

	if n, err := input.Read(buf); err != nil || n != len(buf) || !bytes.Equal(buf, want) {
		t.Fatalf("deadline did not reset: n=%d err=%v", n, err)
	}
}

type seedReadSignal struct {
	reader  io.Reader
	started chan struct{}
}

func (r *seedReadSignal) Read(p []byte) (int, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}

	return r.reader.Read(p)
}

func TestSeedInputCancellationInterruptsAndJoinsRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	reader, _ := deadlineSeedPipe(t, ctx)

	signal := &seedReadSignal{reader: reader, started: make(chan struct{})}
	input := (&seedInput{source: reader, reader: signal, timeout: time.Minute}).Reader(ctx)

	done := make(chan error, 1)
	go func() { _, err := input.Read(make([]byte, 64)); done <- err }()

	select {
	case <-signal.started:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel left source read running")
	}
}

func TestSeedInputDeadlineCoversEntireShortReadSequence(t *testing.T) {
	input, writer := pipeSeedInput(t, t.Context(), 45*time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)

		for range 4 {
			_, _ = writer.Write([]byte{1})

			time.Sleep(20 * time.Millisecond)
		}
	}()

	buf := make([]byte, 4)
	if _, err := input.Read(buf); err == nil {
		t.Fatal("partial reads extended the complete-seed deadline")
	}

	<-done
}

func TestSeedInputReseedFailureRecoveryAndWorkerJoin(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	input, writer := pipeSeedInput(t, ctx, 25*time.Millisecond)

	root := observableTestDRBG(t)
	if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { defer close(done); reseedLoop(ctx, input, 1, 32, root) }()

	t.Cleanup(func() {
		cancel()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("reseed worker did not join")
		}
	})
	waitForReseedState(t, func() bool { return root.GetMetadata().ReseedFailuresTotal > 0 })

	if _, err := root.Read(make([]byte, 1)); !errors.Is(err, drbg.ErrSeedUnavailable) {
		t.Fatalf("timeout did not fail closed: %v", err)
	}

	if _, err := writer.Write(bytes.Repeat([]byte{3}, 32)); err != nil {
		t.Fatal(err)
	}

	waitForReseedState(t, func() bool { return root.GetMetadata().ReseedsTotal > 0 })

	if _, err := root.Read(make([]byte, 1)); err != nil {
		t.Fatalf("successful reseed did not restore output: %v", err)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked reseed was not cancelled")
	}
}
