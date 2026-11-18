package output

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

func TestBlockedPipeCancellationAndPartialCounts(t *testing.T) {
	r, original, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer original.Close()

	fd := original.Fd()

	before, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}

	w, err := openStdout(original)
	if err != nil {
		t.Fatal(err)
	}

	if !w.pollable {
		t.Fatal("pipe cannot be interrupted")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()

	n, err := w.WriteContext(ctx, bytes.Repeat([]byte{0x53}, 2<<20))
	if !errors.Is(err, context.DeadlineExceeded) || n < 0 || n >= 2<<20 || time.Since(started) > time.Second {
		t.Fatalf("blocked pipe: %d %v", n, err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil || before != after {
		t.Fatal("stdout flags were not restored", err)
	}

	if err := original.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := io.ReadAll(r)
	if err != nil || len(got) != n || !bytes.Equal(got, bytes.Repeat([]byte{0x53}, n)) {
		t.Fatalf("partial count differs from pipe: %d/%d, %v", len(got), n, err)
	}
}

func TestBrokenPipeIsAnErrorAndFileAppendIsPreserved(t *testing.T) {
	r, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	w, err := openStdout(pipe)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.WriteContext(t.Context(), []byte{1}); !errors.Is(err, unix.EPIPE) {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "append.bin")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, err := f.WriteString("keep"); err != nil {
		t.Fatal(err)
	}

	w, err = openStdout(f)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.WriteContext(t.Context(), []byte("bytes")); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := f.WriteString("after"); err != nil {
		t.Fatal("export closed caller's stdout", err)
	}

	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keepbytesafter" {
		t.Fatalf("append/offset lost: %q, %v", data, err)
	}
}
