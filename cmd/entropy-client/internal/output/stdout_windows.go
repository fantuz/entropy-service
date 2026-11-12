package output

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func OpenStdout() (*Writer, error) {
	return nil, errors.New("raw stdout export is supported on Linux; use -output=FILE on this platform")
}

func OpenTextStdout() (*Writer, error) { return openTextStdout(os.Stdout) }

// Inherited Windows stdout pipes are synchronous, so Go write deadlines cannot
// interrupt them. PIPE_NOWAIT keeps each write bounded without a blocked worker.
// Only our duplicate is closed, and the pipe's original mode is restored.
func openTextStdout(stdout *os.File) (*Writer, error) {
	original := windows.Handle(stdout.Fd())

	var handle windows.Handle

	process := windows.CurrentProcess()
	if err := windows.DuplicateHandle(process, original, process, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, fmt.Errorf("duplicate report stdout: %w", err)
	}

	file := os.NewFile(uintptr(handle), "report stdout")
	writer := &Writer{file: file}

	kind, err := windows.GetFileType(handle)
	if err != nil {
		return nil, errors.Join(err, writer.Close())
	}

	if kind != windows.FILE_TYPE_PIPE {
		return writer, nil
	}

	var state uint32
	if err := windows.GetNamedPipeHandleState(handle, &state, nil, nil, nil, nil, 0); err != nil {
		return nil, errors.Join(fmt.Errorf("inspect report stdout pipe: %w", err), writer.Close())
	}

	// Some synchronous byte pipes accept a nonblocking write only when the
	// whole chunk fits. A fixed 32 KiB chunk can therefore make no progress
	// forever on the default 4 KiB anonymous pipe, even with a live reader.
	var outbound, inbound uint32
	if err := windows.GetNamedPipeInfo(handle, nil, &outbound, &inbound, nil); err != nil {
		return nil, errors.Join(fmt.Errorf("inspect report stdout capacity: %w", err), writer.Close())
	}

	chunkSize := uint32(writeChunk)

	for _, capacity := range []uint32{outbound, inbound} {
		if capacity > 0 {
			chunkSize = min(chunkSize, capacity)
		}
	}

	mode := state | windows.PIPE_NOWAIT
	if err := windows.SetNamedPipeHandleState(handle, &mode, nil, nil); err != nil {
		return nil, errors.Join(fmt.Errorf("enable cancellable report stdout: %w", err), writer.Close())
	}

	writer.cleanup = func() error { return windows.SetNamedPipeHandleState(handle, &state, nil, nil) }
	writer.writeContext = func(ctx context.Context, data []byte) (int, error) {
		written := 0

		for len(data) > 0 {
			if err := ctx.Err(); err != nil {
				return written, err
			}

			var count uint32

			err := windows.WriteFile(handle, data[:min(len(data), int(chunkSize))], &count, nil)
			written += int(count)
			data = data[count:]

			if err != nil {
				return written, err
			}

			if count == 0 {
				timer := time.NewTimer(time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return written, ctx.Err()
				case <-timer.C:
				}
			}
		}

		return written, nil
	}

	return writer, nil
}
