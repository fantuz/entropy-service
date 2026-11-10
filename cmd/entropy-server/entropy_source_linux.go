//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const sourceOSFallback = "getrandom(GRND_NONBLOCK)"

// Device opens must not wait for data. Deadline support is probed on the opened
// descriptor, so neither pathname races nor unsupported blocking reads slip in.
func openCharacterEntropySource(path string) (io.ReadCloser, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(fd), path)

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("inspect opened source %q: %w", path, err)
	}

	if info.Mode()&os.ModeCharDevice == 0 {
		_ = f.Close()
		return nil, fmt.Errorf("%q is not a character device", path)
	}

	if err := f.SetReadDeadline(time.Time{}); err == nil {
		return f, nil
	}
	// Linux urandom has no poll method but its reads do not wait for entropy.
	// Verify the actual device number; a pathname alone cannot grant this exception.
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && unix.Major(stat.Rdev) == 1 && unix.Minor(stat.Rdev) == 9 {
		return &nonblockingSeedSource{ReadCloser: f}, nil
	}

	_ = f.Close()

	return nil, fmt.Errorf("entropy device %q does not support read deadlines; a pollable, nonblocking driver is required", path)
}

// Only known nonblocking OS sources use this adapter. The full seed reader
// checks its context/deadline between bounded chunks; this is not a device bypass.
type nonblockingSeedSource struct{ io.ReadCloser }

func (*nonblockingSeedSource) SetReadDeadline(time.Time) error { return nil }

type getrandomSeedSource struct{}

func (getrandomSeedSource) Read(p []byte) (int, error) {
	n, err := unix.Getrandom(p, unix.GRND_NONBLOCK)
	if errors.Is(err, unix.EAGAIN) {
		return max(n, 0), errors.New("OS random source is not initialized; refusing to wait indefinitely")
	}

	return max(n, 0), err
}

func (getrandomSeedSource) Close() error { return nil }

func openOSSeedSource() (io.ReadCloser, error) {
	return &nonblockingSeedSource{ReadCloser: getrandomSeedSource{}}, nil
}
