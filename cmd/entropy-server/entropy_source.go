package main

import (
	"errors"
	"fmt"
	"io"
	"log"
)

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

		log.Printf("OS CSPRNG device %q unavailable (%v); trying %s", DefaultCSPRNG, fallbackErr, sourceOSFallback)
	} else {
		log.Printf("OS CSPRNG device %q unavailable (%v); trying %s", DefaultCSPRNG, err, sourceOSFallback)
	}

	// Use the OS cryptographic source without an unbounded initialization wait.
	// No silent fallback is possible once strict mode has been selected.
	fallback, err := openOSSeedSource()

	return fallback, sourceOSFallback, err
}
