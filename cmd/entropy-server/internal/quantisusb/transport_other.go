//go:build (!linux || (!amd64 && !arm && !arm64)) && (!windows || !amd64)

package quantisusb

import "errors"

const Available = false

func Open(string) (*Source, error) {
	return nil, errors.New("native Quantis USB is available on Linux amd64/arm/arm64 and Windows x64")
}
