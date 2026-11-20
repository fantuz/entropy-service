//go:build !linux

package device

import "errors"

// Supported reports whether this binary includes the device writer.
const Supported = false

func Open() (*Writer, error) {
	return nil, errors.New("writeout is supported only on Linux")
}

func OpenDestination(_ string) (*Writer, error) {
	return nil, errors.New("device output is supported only on Linux")
}
