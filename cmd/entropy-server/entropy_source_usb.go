package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/hardware"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/quantisusb"
)

func isUSBEntropySource(path string) bool { return strings.HasPrefix(path, quantisusb.Prefix) }

func openUSBEntropySource(path string) (io.ReadCloser, string, bool, error) {
	_, selected, err := quantisusb.ParseSelector(path)
	if !selected || err != nil {
		return nil, "", selected, err
	}

	if !quantisusb.Available {
		return nil, "", true, errors.New("native Quantis USB transport is unavailable on this platform")
	}

	inventory, err := hardware.Discover()
	if err != nil {
		return nil, "", true, err
	}

	for _, device := range inventory.Devices {
		if device.Family == hardware.USB && device.Source == path && len(device.Paths) > 0 {
			source, err := quantisusb.Open(device.Paths[0])
			if err != nil {
				return nil, "", true, err
			}

			return source, path, true, nil
		}
	}

	return nil, "", true, fmt.Errorf("the Quantis USB source %q is unavailable; check hardware list, USB permissions or WinUSB binding; no OS fallback", path)
}
