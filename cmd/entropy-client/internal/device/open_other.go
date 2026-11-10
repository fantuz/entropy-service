//go:build !linux

package device

import "errors"

func Open() (*Writer, error) {
	return nil, errors.New("writeout is supported only on Linux")
}

func OpenDestination(_ string) (*Writer, error) {
	return nil, errors.New("device output is supported only on Linux")
}
