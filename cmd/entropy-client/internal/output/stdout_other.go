//go:build !linux

package output

import "errors"

func OpenStdout() (*Writer, error) {
	return nil, errors.New("raw stdout export is supported on Linux; use -output=FILE on this platform")
}
