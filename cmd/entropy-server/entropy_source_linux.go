//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	DefaultCSPRNG    = "/dev/urandom"
	sourceOSFallback = "getrandom(GRND_NONBLOCK)"
)

func openEntropySource(path string, require bool) (io.ReadCloser, string, error) {
	if source, actual, selected, err := openUSBEntropySource(path); selected {
		return source, actual, err
	}

	if isChaosKeyDevicePath(path) {
		require = true
	} // Explicit ChaosKey requests never select OS fallback.

	return selectEntropySource(path, require, openCharacterEntropySource)
}

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

	stat, known := info.Sys().(*syscall.Stat_t)
	if known && unix.Major(stat.Rdev) == 189 {
		_ = f.Close()
		return nil, errors.New("raw USB device nodes expose descriptors, not entropy; use a supported quantis-usb:N or chaoskey-usb:N transport")
	}

	if known && unix.Major(stat.Rdev) == 1 && (unix.Minor(stat.Rdev) == 8 || unix.Minor(stat.Rdev) == 9) {
		// Some kernels wait during random initialization even for nonblocking
		// device reads. Check readiness via the nonblocking syscall first.
		if err := checkOSSeedReady(unix.Getrandom); err != nil {
			_ = f.Close()
			return nil, err
		}
	}

	if err := f.SetReadDeadline(time.Time{}); err == nil {
		return f, nil
	}
	// Initialized Linux urandom has no poll method; its reads do not wait for entropy.
	// Verify the actual device number; a pathname alone cannot grant this exception.
	if known && unix.Major(stat.Rdev) == 1 && unix.Minor(stat.Rdev) == 9 {
		return &nonblockingSeedSource{ReadCloser: f}, nil
	}

	_ = f.Close()

	if isChaosKeyDevicePath(path) {
		return nil, fmt.Errorf("ChaosKey kernel interface %q lacks read deadlines: a pollable, nonblocking driver is required; direct cooked USB uses chaoskey-usb:N after the operator releases kernel-driver ownership (see docs/chaoskey.md)", path)
	}

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

// An initialized Linux CSPRNG does not become uninitialized again. Requiring
// readiness avoids early-boot device waits without ever consuming unsafe seed.
func checkOSSeedReady(getrandom func([]byte, int) (int, error)) error {
	var probe [1]byte

	n, err := getrandom(probe[:], unix.GRND_NONBLOCK)
	clear(probe[:])

	if err != nil {
		return fmt.Errorf("OS random initialization is not ready for bounded seeding: %w", err)
	}

	if n != len(probe) {
		return io.ErrUnexpectedEOF
	}

	return nil
}

func isChaosKeyDevicePath(path string) bool {
	return strings.HasPrefix(path, "/dev/") && strings.HasPrefix(filepath.Base(path), "chaoskey")
}
