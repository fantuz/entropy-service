package output

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func OpenStdout() (*Writer, error) { return openStdout(os.Stdout) }

// OpenTextStdout also allows human-readable reports to a terminal.
func OpenTextStdout() (*Writer, error) { return openTextStdout(os.Stdout) }

func openStdout(stdout *os.File) (*Writer, error)     { return duplicateStdout(stdout, false) }
func openTextStdout(stdout *os.File) (*Writer, error) { return duplicateStdout(stdout, true) }

// Duplicate stdout to own its lifetime without closing the caller's descriptor.
// For a pipe, temporarily enable nonblocking I/O so Go deadlines can cancel a
// blocked write. Restore its shared descriptor flags on close.
func duplicateStdout(stdout *os.File, text bool) (*Writer, error) {
	if _, err := unix.IoctlGetTermios(int(stdout.Fd()), unix.TCGETS); !text && err == nil {
		return nil, errors.New("refusing raw binary output to a terminal; redirect stdout to a file or pipe")
	}

	flags, err := unix.FcntlInt(stdout.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return nil, fmt.Errorf("inspect stdout: %w", err)
	}

	fd, err := unix.FcntlInt(stdout.Fd(), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, fmt.Errorf("duplicate stdout: %w", err)
	}

	info, err := stdout.Stat()
	if err != nil {
		return nil, errors.Join(err, unix.Close(fd))
	}

	isPipe := info.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0
	if isPipe {
		if err := unix.SetNonblock(fd, true); err != nil {
			return nil, errors.Join(err, unix.Close(fd))
		}
	}

	f := os.NewFile(uintptr(fd), "stdout export")

	w := &Writer{file: f, pollable: f.SetWriteDeadline(time.Time{}) == nil}
	if isPipe {
		// Capture fd, not File.Fd(): Fd can switch a pollable file to blocking.
		w.cleanup = func() error { return unix.SetNonblock(fd, flags&unix.O_NONBLOCK != 0) }
	}

	if isPipe && !w.pollable {
		return nil, errors.Join(errors.New("stdout pipe does not support cancellable writes"), w.Close())
	}

	return w, nil
}
