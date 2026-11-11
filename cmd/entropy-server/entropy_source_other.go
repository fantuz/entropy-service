//go:build !linux && !windows

package main

import (
	"errors"
	"io"
)

const (
	DefaultCSPRNG    = "/dev/urandom"
	sourceOSFallback = "unsupported OS source"
)

func openEntropySource(path string, require bool) (io.ReadCloser, string, error) {
	return selectEntropySource(path, require, openCharacterEntropySource)
}

func openCharacterEntropySource(string) (io.ReadCloser, error) {
	return nil, errors.New("bounded entropy-device I/O currently requires Linux")
}

func openOSSeedSource() (io.ReadCloser, error) {
	return nil, errors.New("bounded OS entropy fallback currently requires Linux")
}
