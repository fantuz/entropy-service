package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
)

// openCharacterEntropySource checks the actual opened file, not a prior Stat
// of its pathname. This avoids accepting a replaced regular file or reporting
// a device that was never successfully opened.
func openCharacterEntropySource(path string) (io.ReadCloser, error) {
	source, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	info, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return nil, fmt.Errorf("inspect opened source %q: %w", path, err)
	}

	if info.Mode()&os.ModeCharDevice == 0 {
		_ = source.Close()
		return nil, fmt.Errorf("%q is not a character device", path)
	}

	return source, nil
}

// openEntropySource returns the selected reader and its *actual* origin.
// -require-device requires the explicitly named source to open successfully;
// fallback is never attempted in strict mode. Neither mode verifies the
// entropy quality or hardware identity of a character device.
func openEntropySource(path string, require bool) (io.ReadCloser, string, error) {
	return selectEntropySource(path, require, openCharacterEntropySource)
}

// selectEntropySource accepts an opener so both successful selection and
// open-time races/failures can be tested without live hardware.
func selectEntropySource(
	path string, require bool, open func(string) (io.ReadCloser, error),
) (io.ReadCloser, string, error) {
	if path == "" {
		if require {
			return nil, "", errors.New("required entropy device path must not be empty")
		}

		path = DefaultCSPRNG
	}

	source, err := open(path)
	if err == nil {
		return source, path, nil
	}

	if require {
		return nil, "", fmt.Errorf("open required entropy device %q: %w", path, err)
	}

	if path != DefaultCSPRNG {
		log.Printf("entropy device %q unavailable (%v); trying OS CSPRNG %q", path, err, DefaultCSPRNG)

		fallback, fallbackErr := open(DefaultCSPRNG)
		if fallbackErr == nil {
			return fallback, DefaultCSPRNG, nil
		}

		log.Printf("OS CSPRNG device %q unavailable (%v); using Go crypto/rand", DefaultCSPRNG, fallbackErr)
	} else {
		log.Printf("OS CSPRNG device %q unavailable (%v); using Go crypto/rand", DefaultCSPRNG, err)
	}

	// This is the Go runtime's cryptographic OS source, not a weak PRNG.
	// No silent fallback is possible once strict mode has been selected.
	return io.NopCloser(rand.Reader), "crypto/rand", nil
}
