//go:build !linux

package main

import (
	"errors"
	"io"
)

const sourceOSFallback = "unsupported OS source"

func openCharacterEntropySource(string) (io.ReadCloser, error) {
	return nil, errors.New("bounded entropy-device I/O currently requires Linux")
}

func openOSSeedSource() (io.ReadCloser, error) {
	return nil, errors.New("bounded OS entropy fallback currently requires Linux")
}
