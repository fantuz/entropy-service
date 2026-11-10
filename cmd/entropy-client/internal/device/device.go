// Package device mixes bytes into Linux's random input pool without crediting entropy.
package device

import (
	"context"
	"fmt"
	"io"
	"os"
)

const writeChunk = 32 * 1024

// Writer owns one validated /dev/urandom descriptor. Use it from one goroutine;
// Close must follow WriteContext, rather than racing an in-flight write.
type Writer struct {
	file io.WriteCloser
}

// WriteContext accounts for accepted bytes, including partial failed writes.
// Chunking bounds work between cancellation checks; it is not a hard deadline
// for a kernel system call. No entropy-crediting ioctl is used.
func (w *Writer) WriteContext(ctx context.Context, data []byte) (int, error) {
	if w == nil || w.file == nil {
		return 0, fmt.Errorf("write /dev/urandom: %w", os.ErrClosed)
	}

	written := 0

	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return written, err
		}

		chunk := data[:min(len(data), writeChunk)]

		n, err := w.file.Write(chunk)
		if n < 0 || n > len(chunk) {
			return written, fmt.Errorf("write /dev/urandom: invalid byte count %d", n)
		}

		written += n
		if err != nil {
			return written, fmt.Errorf("write /dev/urandom: %w", err)
		}

		if n != len(chunk) {
			return written, fmt.Errorf("write /dev/urandom: %w", io.ErrShortWrite)
		}

		data = data[n:]
	}

	return written, nil
}

func (w *Writer) Close() error {
	if w == nil || w.file == nil {
		return nil
	}

	file := w.file

	w.file = nil
	if err := file.Close(); err != nil {
		return fmt.Errorf("close /dev/urandom: %w", err)
	}

	return nil
}
