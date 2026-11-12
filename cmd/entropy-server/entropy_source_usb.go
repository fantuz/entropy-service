package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/chaoskey"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/hardware"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/quantisusb"
)

func isUSBEntropySource(path string) bool { return strings.HasPrefix(path, quantisusb.Prefix) }

func isChaosKeyEntropySource(path string) bool { return strings.HasPrefix(path, chaoskey.Prefix) }

func openUSBEntropySource(path string) (io.ReadCloser, string, bool, error) {
	if _, selected, err := chaoskey.ParseSelector(path); selected {
		if err != nil {
			return nil, "", true, err
		}

		return openHardwareUSB(path, hardware.ChaosKey, chaoskey.Available, func(path string) (io.ReadCloser, error) { return chaoskey.Open(path) })
	}

	_, selected, err := quantisusb.ParseSelector(path)
	if !selected || err != nil {
		return nil, "", selected, err
	}

	return openHardwareUSB(path, hardware.USB, quantisusb.Available, func(path string) (io.ReadCloser, error) { return quantisusb.Open(path) })
}

func openHardwareUSB(path, family string, available bool, open func(string) (io.ReadCloser, error)) (io.ReadCloser, string, bool, error) {
	if !available {
		return nil, "", true, errors.New("native USB transport is unavailable on this platform")
	}

	inventory, err := hardware.Discover()
	if err != nil {
		return nil, "", true, err
	}

	for _, device := range inventory.Devices {
		if device.Family == family && device.Source == path && len(device.Paths) > 0 {
			source, err := open(device.Paths[0])
			if err != nil {
				return nil, "", true, err
			}

			return source, path, true, nil
		}
	}

	return nil, "", true, fmt.Errorf("the %s source %q is unavailable; check hardware list, USB permissions/ownership or WinUSB binding; no OS fallback", family, path)
}
