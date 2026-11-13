package lab

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDeviceFileAndRandom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []string{path, "/dev/urandom"} {
		registry := testRegistry(t, Config{ID: "device", Kind: "device", Endpoint: endpoint})

		stream, err := registry.Open(t.Context(), "device", 32, 20)
		if err != nil {
			t.Fatal(err)
		}

		data, err := stream.Read(t.Context(), 32)
		_ = stream.Close()

		if endpoint == path {
			if !errors.Is(err, io.EOF) || !bytes.Equal(data, []byte("fixture")) {
				t.Fatalf("file read failed: %q %v", data, err)
			}
		} else if err != nil || len(data) != 32 {
			t.Fatalf("device read failed: %v", err)
		}
	}
}

func labFIFOPair(t *testing.T) (*deviceReader, *os.File) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "pending.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	writer, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := openDevice(path)
	if err != nil {
		_ = writer.Close()

		t.Fatal(err)
	}

	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })

	return reader, writer
}

func TestDeviceCloseInterruptsReadWithoutParentCancellation(t *testing.T) {
	reader, _ := labFIFOPair(t)
	done := make(chan error, 1)

	go func() { _, err := reader.Read(t.Context(), 32); done <- err }()

	waitDeviceReadLocked(t, reader)

	closed := make(chan error, 1)
	go func() { closed <- reader.Close() }()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited behind the read mutex")
	}

	select {
	case err := <-done:
		if !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, os.ErrClosed) {
			t.Fatalf("pending read after close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt the pending file read")
	}

	if t.Context().Err() != nil {
		t.Fatal("the test cancelled the parent instead of exercising Close")
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}

	if data, err := reader.Read(t.Context(), 32); !errors.Is(err, io.ErrClosedPipe) || len(data) != 0 {
		t.Fatal("closed reader exposed data")
	}
}

func waitDeviceReadLocked(t *testing.T, reader *deviceReader) {
	t.Helper()

	deadline := time.Now().Add(time.Second)

	for reader.readMu.TryLock() {
		reader.readMu.Unlock()

		if time.Now().After(deadline) {
			t.Fatal("device read did not start")
		}

		time.Sleep(time.Millisecond)
	}
}

func TestDeviceParentCancellationPreservesHandleForNextRead(t *testing.T) {
	reader, writer := labFIFOPair(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		data, err := reader.Read(ctx, 32)
		if len(data) != 0 {
			done <- errors.New("cancelled read retained partial data")
			return
		}

		done <- err
	}()

	waitDeviceReadLocked(t, reader)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt the pending LAB read")
	}

	want := bytes.Repeat([]byte{7}, 8)
	if _, err := writer.Write(want); err != nil {
		t.Fatal(err)
	}

	got, err := reader.Read(t.Context(), len(want))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("cancelled read closed or corrupted its reusable handle: %v", err)
	}
}

func TestDeviceReadCancellationAndDeadlineResetOnSameHandle(t *testing.T) {
	reader, writer := labFIFOPair(t)
	for range 4 {
		if _, err := writer.Write([]byte{1, 2}); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		data, err := reader.Read(ctx, 8)

		cancel()

		if !errors.Is(err, context.DeadlineExceeded) || len(data) != 0 {
			t.Fatalf("timed-out partial sample retained: bytes=%d err=%v", len(data), err)
		}

		want := bytes.Repeat([]byte{7}, 8)
		if _, err := writer.Write(want); err != nil {
			t.Fatal(err)
		}

		data, err = reader.Read(t.Context(), len(want))
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("old callback/deadline spoiled the next read: %v", err)
		}
	}
}

func TestDeviceRejectsUnpollableUnqualifiedCharacterSource(t *testing.T) {
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}

	// Use a real nonpollable handle with synthetic unknown-driver identity.
	// Physical drivers must separately qualify their actual read contract.
	_, err = wrapDeviceFile(file, unix.Stat_t{Mode: unix.S_IFCHR, Rdev: unix.Mkdev(240, 0)})
	if !errors.Is(err, os.ErrNoDeadline) {
		t.Fatalf("unqualified source was admitted: %v", err)
	}

	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("rejected device leaked its file descriptor")
	}
}

func TestDeviceDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	writer, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	registry := testRegistry(t, Config{ID: "fifo", Kind: "device", Endpoint: path, TimeoutMs: 100})

	stream, err := registry.Open(t.Context(), "fifo", 32, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	started := time.Now()

	if _, err := stream.Read(t.Context(), 32); err == nil {
		t.Fatal("empty FIFO read ignored timeout")
	}

	if time.Since(started) > time.Second {
		t.Fatal("device read did not stop at its deadline")
	}
}
