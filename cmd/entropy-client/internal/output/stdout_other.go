//go:build !linux && !windows && !darwin

package output

import (
	"errors"
	"os"
)

// RawStdoutSupported reports whether this binary can export binary samples to stdout.
const RawStdoutSupported = false

func OpenStdout() (*Writer, error) {
	return nil, errors.New("raw stdout export is supported on Linux; use -output=FILE on this platform")
}

// Other platforms retain text stdout; filesystem/console syscalls check context
// between chunks. Linux and Windows pipe adapters provide cancellable writes.
func OpenTextStdout() (*Writer, error) { return &Writer{file: os.Stdout, keepOpen: true}, nil }
