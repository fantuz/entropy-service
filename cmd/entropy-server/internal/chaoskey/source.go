// Package chaoskey reads the ChaosKey firmware's cooked endpoint, never its raw
// ADC or flash endpoints. This experimental source does not certify device health.
package chaoskey

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/usbtransport"
)

const (
	Prefix     = "chaoskey-usb:"
	Available  = usbtransport.Available
	packetSize = 64
	maxRead    = 32 << 10
)

func ParseSelector(value string) (index int, selected bool, err error) {
	if !strings.HasPrefix(value, "chaoskey") {
		return 0, false, nil
	}

	text := strings.TrimPrefix(value, Prefix)

	index, err = strconv.Atoi(text)
	if err != nil || index < 0 || index > 126 || strconv.Itoa(index) != text {
		return 0, true, fmt.Errorf("invalid ChaosKey USB source %q; use -device=chaoskey-usb:0 (index 0..126 from hardware list)", value)
	}

	return index, true, nil
}

type Source struct {
	readMu   sync.Mutex
	mu       sync.Mutex
	device   usbtransport.Transport
	deadline time.Time
	closed   bool
	closeErr error
}

func Open(path string) (*Source, error) {
	device, packet, err := usbtransport.Open(path, usbtransport.ChaosKey)
	if err != nil {
		return nil, err
	}

	return newSource(device, packet)
}

func newSource(device usbtransport.Transport, packet int) (*Source, error) {
	if packet != packetSize {
		return nil, errors.Join(errors.New("ChaosKey requires the referenced 64-byte cooked bulk endpoint"), device.Close())
	}

	return &Source{device: device}, nil
}

func (s *Source) stateError() error {
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

func (s *Source) SetReadDeadline(deadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return os.ErrClosed
	}

	s.deadline = deadline

	return s.device.SetReadDeadline(deadline)
}

func (s *Source) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	err := s.device.Close() // Cancel and join native I/O before releasing Read buffers.
	s.readMu.Lock()
	defer s.readMu.Unlock()

	s.closeErr = err

	return s.closeErr
}

func (s *Source) Read(p []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	if err := s.stateError(); err != nil {
		return 0, err
	}

	if len(p) == 0 {
		return 0, nil
	}

	size := min(len(p), maxRead)

	buffer := make([]byte, ((size+packetSize-1)/packetSize)*packetSize)
	defer clear(buffer)

	n, err := s.device.Bulk(buffer)
	if err != nil {
		return 0, err
	} // Never deliver a timed-out/failed acquisition's prefix.

	if err := s.stateError(); err != nil {
		return 0, err
	}

	if n < 0 || n > len(buffer) {
		return 0, errors.New("invalid ChaosKey cooked transfer length")
	}

	if n == 0 {
		return 0, io.ErrNoProgress
	}
	// Short bulk transfers are valid. Unused tails are discarded, never saved for
	// a later reseed. The shared seed reader fills its bounded seed if necessary.
	return copy(p, buffer[:n]), nil
}
