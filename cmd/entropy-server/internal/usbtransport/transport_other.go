//go:build (!linux || (!amd64 && !arm && !arm64)) && (!windows || !amd64)

package usbtransport

import "errors"

const Available = false

func Open(string, Profile) (Transport, int, error) {
	return nil, 0, errors.New("native USB is available on Linux amd64/arm/arm64 and Windows x64")
}
