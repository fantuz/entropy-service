package main

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
)

const (
	DefaultCSPRNG    = config.WindowsCSPRNG
	sourceOSFallback = "Windows OS CSPRNG (crypto/rand)"
)

func openEntropySource(path string, require bool) (io.ReadCloser, string, error) {
	if require {
		return nil, "", errors.New("-require-device is unavailable on Windows; use the Windows OS CSPRNG")
	}

	if path != "" && path != DefaultCSPRNG {
		return nil, "", errors.New("direct entropy devices are unavailable on Windows; use -device=windows-csprng")
	}

	source, err := openOSSeedSource()

	return source, DefaultCSPRNG, err
}

func openCharacterEntropySource(string) (io.ReadCloser, error) {
	return nil, errors.New("direct QRNG/device access is unavailable on Windows; use -device=windows-csprng")
}

func openOSSeedSource() (io.ReadCloser, error) {
	return &windowsSeedSource{}, nil
}

// The native OS RNG is synchronous. Deadlines/close are checked between bounded
// reads and after each call; they cannot interrupt an OS call already in flight.
// No worker is abandoned to simulate cancellation or silently bypass a deadline.
type windowsSeedSource struct {
	mu       sync.Mutex
	deadline time.Time
	closed   bool
}

func (s *windowsSeedSource) stateError() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return os.ErrClosed
	}

	if !s.deadline.IsZero() && !time.Now().Before(s.deadline) {
		return os.ErrDeadlineExceeded
	}

	return nil
}

func (s *windowsSeedSource) Read(p []byte) (int, error) {
	if err := s.stateError(); err != nil {
		return 0, err
	}

	n, err := rand.Read(p[:min(len(p), 32<<10)])
	if stateErr := s.stateError(); stateErr != nil {
		clear(p[:n])

		return 0, stateErr
	}

	return n, err
}

func (s *windowsSeedSource) SetReadDeadline(deadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return os.ErrClosed
	}

	s.deadline = deadline

	return nil
}

func (s *windowsSeedSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	return nil
}
