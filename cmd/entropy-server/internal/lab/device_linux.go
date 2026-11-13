package lab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// readMu serializes reads/deadline resets only. Close must not acquire it:
// os.File owns descriptor references and safely wakes its runtime poller.
type deviceReader struct {
	readMu   sync.Mutex
	file     *os.File
	pollable bool
	closed   atomic.Bool
}

func openDevice(path string) (*deviceReader, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	return wrapDeviceFile(os.NewFile(uintptr(fd), path), stat)
}

// Takes ownership even on error. stat comes from the opened descriptor, not
// a pathname: aliases do not grant an unsupported driver an exception.
func wrapDeviceFile(file *os.File, stat unix.Stat_t) (*deviceReader, error) {
	kind := stat.Mode & unix.S_IFMT
	if kind != unix.S_IFCHR && kind != unix.S_IFREG && kind != unix.S_IFIFO {
		_ = file.Close()
		return nil, errors.New("source must be a readable character device, file or FIFO")
	}

	deadlineErr := file.SetReadDeadline(time.Time{})

	pollable := deadlineErr == nil
	if !pollable && kind != unix.S_IFREG && !safeMemoryDevice(stat) {
		_ = file.Close()
		return nil, fmt.Errorf("LAB device requires nonblocking, pollable read deadlines; use a qualified driver or socket helper: %w", deadlineErr)
	}

	if kind == unix.S_IFCHR && unix.Major(stat.Rdev) == 1 && (unix.Minor(stat.Rdev) == 8 || unix.Minor(stat.Rdev) == 9) {
		var probe [1]byte

		n, err := unix.Getrandom(probe[:], unix.GRND_NONBLOCK)
		clear(probe[:])

		if err != nil || n != len(probe) {
			_ = file.Close()
			return nil, errors.New("OS random source is not initialized; refusing a potentially blocking LAB read")
		}
	}

	return &deviceReader{file: file, pollable: pollable}, nil
}

func safeMemoryDevice(stat unix.Stat_t) bool {
	if stat.Mode&unix.S_IFMT != unix.S_IFCHR || unix.Major(stat.Rdev) != 1 {
		return false
	}

	switch unix.Minor(stat.Rdev) {
	case 3, 5, 7, 9: // Linux null, zero, full (zero reads), initialized urandom.
		return true
	default:
		return false
	}
}

func (d *deviceReader) Close() error {
	if d.closed.Swap(true) {
		return nil
	}

	return d.file.Close()
}

func (d *deviceReader) Read(ctx context.Context, count int) ([]byte, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()

	if d.closed.Load() {
		return nil, io.ErrClosedPipe
	}

	if err := readContextError(ctx); err != nil {
		return nil, err
	}

	if d.pollable {
		deadline, _ := ctx.Deadline()
		if err := d.file.SetReadDeadline(deadline); err != nil {
			return nil, err
		}

		interrupted := make(chan struct{})

		stop := context.AfterFunc(ctx, func() {
			_ = d.file.SetReadDeadline(time.Now())

			close(interrupted)
		})
		defer func() {
			if !stop() {
				<-interrupted
			}

			_ = d.file.SetReadDeadline(time.Time{})
		}()
	}

	data := make([]byte, count)

	size := 0
	for size < count {
		if err := readContextError(ctx); err != nil {
			clear(data)
			return nil, err
		}

		n, err := d.file.Read(data[size:min(count, size+(32<<10))])
		size += n

		if contextErr := readContextError(ctx); contextErr != nil {
			clear(data)
			return nil, contextErr
		}

		if d.closed.Load() {
			clear(data)
			return nil, io.ErrClosedPipe
		}

		if err != nil {
			return data[:size], err
		}

		if n == 0 {
			return data[:size], io.ErrNoProgress
		}
	}

	return data, nil
}
