// Package quantisusb implements the referenced Quantis USB 0aba:0102 protocol.
// Protocol reference: ID Quantique Software 20.2.4 QuantisUsb_Commands.h and
// QuantisUsb_{Unix,Windows} sources. See QUANTIS_NOTICES.txt.
package quantisusb

import (
	"encoding/binary"
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
	Prefix        = "quantis-usb:"
	Endpoint      = 0x86
	requestBoard  = 0x10
	requestStatus = 0x13
	requestMask   = 0x15
	maxRead       = 32 << 10
)

type transport = usbtransport.Transport

// ParseSelector uses canonical indices 0..126, not arbitrary USB device paths.
func ParseSelector(value string) (index int, selected bool, err error) {
	if !strings.HasPrefix(value, "quantis-usb") {
		return 0, false, nil
	}

	text := strings.TrimPrefix(value, Prefix)

	index, err = strconv.Atoi(text)
	if err != nil || index < 0 || index > 126 || strconv.Itoa(index) != text {
		return 0, true, fmt.Errorf("invalid Quantis USB source %q; use -device=quantis-usb:0 (index 0..126 from hardware list)", value)
	}

	return index, true, nil
}

// Source owns one claimed USB interface. All status and sample transfers share
// the caller's deadline. No reset, module-enable or acquisition-mode writes occur.
type Source struct {
	readMu     sync.Mutex
	mu         sync.Mutex
	device     transport
	packetSize int
	deadline   time.Time
	closed     bool
	closeErr   error
}

func newSource(device transport, packetSize int) (*Source, error) {
	if packetSize != 64 && packetSize != 512 {
		return nil, errors.Join(errors.New("unsupported Quantis USB bulk packet size"), device.Close())
	}

	return &Source{device: device, packetSize: packetSize}, nil
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

	err := s.device.Close() // Interrupt and join native I/O before joining Read.
	s.readMu.Lock()
	defer s.readMu.Unlock()

	s.closeErr = err

	return s.closeErr
}

func (s *Source) ProbeStatus() (map[string]uint32, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	return s.status()
}

func (s *Source) status() (map[string]uint32, error) {
	status := map[string]uint32{}

	for _, query := range []struct {
		name string
		code byte
		size int
	}{
		{"modules_mask", requestMask, 1},
		{"modules_status", requestStatus, 1},
		{"board_version_raw", requestBoard, 4},
	} {
		if err := s.stateError(); err != nil {
			return status, err
		}

		buffer := make([]byte, query.size)

		n, err := s.device.Control(query.code, buffer)
		if err != nil {
			return status, err
		}

		if n != len(buffer) {
			return status, io.ErrUnexpectedEOF
		}

		value := uint32(buffer[0])
		if len(buffer) == 4 {
			value = binary.LittleEndian.Uint32(buffer)
		}

		status[query.name] = value
	}

	// The referenced USB product has one module. Reject unknown layouts.
	if status["modules_mask"] != 1 || status["modules_status"] != 1 {
		return status, errors.New("the Quantis USB module must be present, enabled and healthy")
	}

	return status, s.stateError()
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

	if _, err := s.status(); err != nil {
		return 0, err
	}

	buffer := make([]byte, min(len(p), maxRead))
	defer clear(buffer)

	packet := make([]byte, s.packetSize)
	defer clear(packet)

	for offset := 0; offset < len(buffer); {
		if err := s.stateError(); err != nil {
			return 0, err
		}

		n, err := s.device.Bulk(packet)
		if err != nil {
			return 0, err
		}

		if n == 0 {
			return 0, io.ErrNoProgress
		}

		if n != len(packet) {
			return 0, io.ErrUnexpectedEOF
		}

		offset += copy(buffer[offset:], packet)
	}

	var finalStatus [1]byte

	if err := s.stateError(); err != nil {
		return 0, err
	}

	if n, err := s.device.Control(requestStatus, finalStatus[:]); err != nil {
		return 0, err
	} else if n != 1 || finalStatus[0] != 1 {
		return 0, errors.New("the Quantis USB module became unhealthy during the read")
	}

	if err := s.stateError(); err != nil {
		return 0, err
	}

	// Unused packet tails are discarded/cleared, never saved for a later reseed.
	return copy(p, buffer), nil
}

const Available = usbtransport.Available

func Open(path string) (*Source, error) {
	device, packet, err := usbtransport.Open(path, usbtransport.Quantis)
	if err != nil {
		return nil, err
	}

	return newSource(device, packet)
}
