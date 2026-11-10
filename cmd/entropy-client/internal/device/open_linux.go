package device

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Open opens the fixed Linux destination once, without creating/truncating a
// file or following a symlink. Validate the actual descriptor before any write.
func Open() (*Writer, error) {
	return openDevice("/dev/urandom")
}

func openDevice(path string) (*Writer, error) {
	return openValidated(path, 9)
}

// OpenDestination permits only Linux random (1:8), urandom (1:9) and null
// (1:3). An explicit destination never grants access to arbitrary hardware,
// block devices, memory devices or files, even if the process is privileged.
func OpenDestination(path string) (*Writer, error) {
	var minor uint32

	switch path {
	case "/dev/random":
		minor = 8
	case "/dev/urandom":
		minor = 9
	case "/dev/null":
		minor = 3
	default:
		return nil, errors.New("output-device must be /dev/random, /dev/urandom or /dev/null")
	}

	return openValidated(path, minor)
}

func openValidated(path string, expectedMinor uint32) (*Writer, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open output device: %w", err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nil, errors.Join(fmt.Errorf("inspect output device: %w", err), file.Close())
	}

	if stat.Mode&unix.S_IFMT != unix.S_IFCHR || unix.Major(stat.Rdev) != 1 || unix.Minor(stat.Rdev) != expectedMinor {
		return nil, errors.Join(fmt.Errorf("output device requires Linux character identity 1:%d", expectedMinor), file.Close())
	}

	return &Writer{file: file}, nil
}
