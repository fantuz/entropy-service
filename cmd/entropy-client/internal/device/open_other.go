//go:build !linux

package device

import "errors"

func Open() (*Writer, error) {
	return nil, errors.New("writeout is supported only on Linux")
}
