// Package output exports raw bytes to a new file or to an existing stdout pipe.
package output

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const writeChunk = 32 * 1024

// Writer has one owner. Close follows the last WriteContext call.
type Writer struct {
	file     *os.File
	syncFile bool
	pollable bool
	cleanup  func() error
}

func OpenFile(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create output: %w", err)
	}

	return &Writer{file: f, syncFile: true}, nil
}

// WriteContext preserves byte order without an additional buffering goroutine.
// Pollable stdout pipes are interrupted on cancellation; regular filesystem
// syscalls can only check cancellation between chunks.
func (w *Writer) WriteContext(ctx context.Context, data []byte) (int, error) {
	if w == nil || w.file == nil {
		return 0, os.ErrClosed
	}

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if w.pollable {
		defer w.interruptOnCancel(ctx)()
	}

	written := 0

	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return written, err
		}

		chunk := data[:min(len(data), writeChunk)]
		n, err := w.file.Write(chunk)
		written += n

		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) && ctx.Err() != nil {
				return written, ctx.Err()
			}

			return written, err
		}

		if n != len(chunk) {
			return written, io.ErrShortWrite
		}

		data = data[n:]
	}

	return written, nil
}

func (w *Writer) interruptOnCancel(ctx context.Context) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = w.file.SetWriteDeadline(time.Now())

		close(done)
	})

	return func() {
		if !stop() {
			<-done
		}

		_ = w.file.SetWriteDeadline(time.Time{})
	}
}

func (w *Writer) Close() error {
	if w == nil || w.file == nil {
		return nil
	}

	f := w.file
	w.file = nil

	var err error
	if w.syncFile {
		err = f.Sync()
	}

	if w.cleanup != nil {
		err = errors.Join(err, w.cleanup())
	}

	return errors.Join(err, f.Close())
}
