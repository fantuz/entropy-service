// Package usbtransport owns bounded native USB I/O for allowlisted hardware.
// Source protocols and health policy remain in their own packages.
package usbtransport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

type Profile uint8

const (
	Quantis Profile = iota
	ChaosKey
	// ChaosKeyRaw is diagnostic capture only, never a server seed selector.
	ChaosKeyRaw
)

type Transport interface {
	Control(request byte, buffer []byte) (int, error)
	Bulk(buffer []byte) (int, error)
	SetReadDeadline(deadline time.Time) error
	Close() error
}

func (p Profile) Name() string {
	if p == Quantis {
		return "Quantis USB"
	}

	if p == ChaosKey {
		return "ChaosKey cooked output"
	}

	if p == ChaosKeyRaw {
		return "ChaosKey raw samples"
	}

	return "unsupported USB profile"
}

func (p Profile) Endpoint() byte {
	if p == Quantis || p == ChaosKeyRaw {
		return 0x86
	}

	if p == ChaosKey {
		return 0x85
	}

	return 0
}

func (p Profile) SupportsPacket(size int) bool {
	return (p == Quantis && (size == 64 || size == 512)) || ((p == ChaosKey || p == ChaosKeyRaw) && size == 64)
}

func (p Profile) ValidateDevice(descriptor []byte) error {
	var vendor, product uint16

	switch p {
	case Quantis:
		vendor, product = 0x0aba, 0x0102
	case ChaosKey, ChaosKeyRaw:
		vendor, product = 0x1d50, 0x60c6
	default:
		return errors.New("unsupported USB profile")
	}

	if len(descriptor) < 18 || descriptor[0] != 18 || descriptor[1] != 1 || binary.LittleEndian.Uint16(descriptor[8:10]) != vendor || binary.LittleEndian.Uint16(descriptor[10:12]) != product || descriptor[17] != 1 {
		return fmt.Errorf("unsupported identity/configuration; expected %s %04x:%04x with one configuration", p.Name(), vendor, product)
	}

	return nil
}

func ControlSetup(request byte, length int) [8]byte {
	var setup [8]byte

	setup[0] = 0xc1
	setup[1] = request
	binary.LittleEndian.PutUint16(setup[6:8], uint16(length))

	return setup
}

// PacketSize validates the complete configuration; only interface/alternate 0
// and the profile's endpoint can select a stream. ChaosKeyRaw must be explicitly
// requested for diagnostic capture; raw 0x86 and flash 0x87 are never fallbacks.
func (p Profile) PacketSize(config []byte) (int, error) {
	if len(config) < 9 || config[0] != 9 || config[1] != 2 || config[4] != 1 || config[5] != 1 || p.Endpoint() == 0 {
		return 0, fmt.Errorf("unsupported %s configuration", p.Name())
	}

	length := int(binary.LittleEndian.Uint16(config[2:4]))
	if length < 9 || length > len(config) {
		return 0, io.ErrUnexpectedEOF
	}

	validInterface, seenPrimary := false, false
	expected, seen, packet := 0, 0, 0

	for offset := 9; offset < length; {
		if offset+2 > length {
			return 0, io.ErrUnexpectedEOF
		}

		size := int(config[offset])
		if size < 2 || offset+size > length {
			return 0, io.ErrUnexpectedEOF
		}

		descriptor := config[offset : offset+size]
		switch descriptor[1] {
		case 4:
			if size < 9 {
				return 0, io.ErrUnexpectedEOF
			}

			if expected != seen {
				return 0, errors.New("USB endpoint count differs from interface descriptor")
			}

			if descriptor[2] != 0 {
				return 0, errors.New("unsupported USB interface number")
			}

			validInterface = descriptor[3] == 0
			if validInterface && seenPrimary {
				return 0, errors.New("duplicate USB primary interface")
			}

			if validInterface {
				seenPrimary = true
			}

			expected, seen = int(descriptor[4]), 0
		case 5:
			if size < 7 || !seenPrimary {
				return 0, io.ErrUnexpectedEOF
			}

			seen++

			if validInterface && descriptor[2] == p.Endpoint() {
				if packet != 0 {
					return 0, errors.New("duplicate USB seed endpoint")
				}

				size := int(binary.LittleEndian.Uint16(descriptor[4:6]))
				if descriptor[3]&3 != 2 || !p.SupportsPacket(size) {
					return 0, errors.New("unsupported USB seed endpoint type/packet size")
				}

				packet = size
			}
		}

		offset += size
	}

	if expected != seen {
		return 0, errors.New("USB endpoint count differs from interface descriptor")
	}

	if packet == 0 {
		return 0, fmt.Errorf("expected %s bulk-IN endpoint 0x%02x", p.Name(), p.Endpoint())
	}

	return packet, nil
}
