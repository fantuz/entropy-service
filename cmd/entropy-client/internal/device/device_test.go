package device

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

type testFile struct {
	write    func([]byte) (int, error)
	closeErr error
	closes   int
}

func (f *testFile) Write(data []byte) (int, error) { return f.write(data) }
func (f *testFile) Close() error                   { f.closes++; return f.closeErr }

func TestWriterChunksWithoutChangingBytes(t *testing.T) {
	data := bytes.Repeat([]byte{0, 1, 127, 255}, writeChunk)

	var out bytes.Buffer

	calls := 0
	file := &testFile{write: func(p []byte) (int, error) {
		calls++

		if len(p) > writeChunk {
			t.Fatal("unbounded kernel write")
		}

		return out.Write(p)
	}}
	w := &Writer{file: file}

	n, err := w.WriteContext(t.Context(), data)
	if err != nil || n != len(data) || calls != 4 || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("incomplete/changed write: bytes=%d calls=%d err=%v", n, calls, err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil || file.closes != 1 {
		t.Fatal("close was not idempotent")
	}

	if _, err := w.WriteContext(t.Context(), data); !errors.Is(err, os.ErrClosed) {
		t.Fatal("write after close did not fail")
	}
}

func TestWriterReportsPartialAndInvalidWrites(t *testing.T) {
	for _, tc := range []struct {
		name      string
		n         int
		err, want error
		accepted  int
	}{
		{"short", 3, nil, io.ErrShortWrite, 3},
		{"empty", 0, nil, io.ErrShortWrite, 0},
		{"partial-error", 3, io.ErrClosedPipe, io.ErrClosedPipe, 3},
		{"failure", 0, os.ErrPermission, os.ErrPermission, 0},
		{"negative", -1, nil, nil, 0},
		{"excess", 100, nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &Writer{file: &testFile{write: func([]byte) (int, error) { return tc.n, tc.err }}}

			n, err := w.WriteContext(t.Context(), make([]byte, 8))
			if err == nil || n != tc.accepted || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("bytes=%d err=%v", n, err)
			}
		})
	}
}

func TestWriterCancellationAndCloseFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	file := &testFile{write: func(p []byte) (int, error) { cancel(); return len(p), nil }, closeErr: io.ErrClosedPipe}
	w := &Writer{file: file}

	n, err := w.WriteContext(ctx, make([]byte, writeChunk*2))
	if n != writeChunk || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost partial accounting: %d %v", n, err)
	}

	if err := w.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("close failure was lost")
	}

	if err := w.Close(); err != nil || file.closes != 1 {
		t.Fatal("failed close retried a descriptor")
	}
}
