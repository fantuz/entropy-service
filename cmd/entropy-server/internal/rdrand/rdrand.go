// Package rdrand provides optional CPU randomness for seed mixing.
package rdrand

import (
	"errors"
	"io"
	"sync"
)

var (
	ErrUnsupported = errors.New("RDRAND is not supported on this CPU or build")
	ErrUnavailable = errors.New("RDRAND failed after one attempt and ten retries")
)

// Available reports whether this build and CPU support RDRAND.
func Available() bool {
	return available
}

// Uint64 returns a CPU-generated word, or an error after bounded retries.
// Zero is a valid result when the instruction reports success.
func Uint64() (uint64, error) {
	if !Available() {
		return 0, ErrUnsupported
	}

	value, ok := readUint64()
	if !ok {
		return 0, ErrUnavailable
	}

	return value, nil
}

// Mixer supplements an existing entropy reader; it never replaces that source.
// Concurrent use is supported only if the underlying reader supports it.
type Mixer struct {
	source    io.Reader
	next      func() (uint64, error)
	onFailure func(error)
	warnOnce  sync.Once
}

// NewMixer requires CPU support. onFailure, if non-nil, reports the first
// exhausted hardware retry budget. Source read errors are returned normally.
func NewMixer(source io.Reader, onFailure func(error)) (*Mixer, error) {
	if !Available() {
		return nil, ErrUnsupported
	}

	return &Mixer{source: source, next: Uint64, onFailure: onFailure}, nil
}

// Read first reads the configured source, then XORs successful CPU samples into
// the bytes obtained. On a hardware failure, the remaining source bytes are
// retained. A failed CPU sample is never consumed as entropy.
func (m *Mixer) Read(p []byte) (int, error) {
	n, err := m.source.Read(p)
	for offset := 0; offset < n; offset += 8 {
		value, hardwareErr := m.next()
		if hardwareErr != nil {
			m.warnOnce.Do(func() {
				if m.onFailure != nil {
					m.onFailure(hardwareErr)
				}
			})

			break
		}

		for i := offset; i < min(offset+8, n); i++ {
			p[i] ^= byte(value)
			value >>= 8
		}
	}

	return n, err
}
