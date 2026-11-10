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
	file, err := os.OpenFile(path, os.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/urandom: %w", err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nil, errors.Join(fmt.Errorf("inspect /dev/urandom: %w", err), file.Close())
	}

	if stat.Mode&unix.S_IFMT != unix.S_IFCHR || unix.Major(stat.Rdev) != 1 || unix.Minor(stat.Rdev) != 9 {
		return nil, errors.Join(errors.New("writeout requires the Linux /dev/urandom character device (1:9)"), file.Close())
	}

	return &Writer{file: file}, nil
}
