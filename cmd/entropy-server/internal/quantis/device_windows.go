// Package quantis implements the legacy Quantis PCI user-mode driver protocol.
// ABI reference: ID Quantique's QuantisPci_Windows.c and Public.h (20.2.4),
// https://github.com/lfd/idq-driver. See QUANTIS_NOTICES.txt and docs/quantis.md.
package quantis

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
)

// This is the legacy PCI interface, not the newer XDMA PCIe interface.
var interfaceGUID = windows.GUID{
	Data1: 0xf2f5f1e5, Data2: 0x380e, Data3: 0x4194,
	Data4: [8]byte{0x8f, 0xc0, 0xed, 0x5a, 0xf6, 0x3e, 0x93, 0xdf},
}

// Open opens one installed legacy 179a:0001 PCI board exclusively. The board
// number follows sorted Windows interface paths, not the SDK's mixed PCI/PCIe
// numbering. Explicit hardware selection never falls back to the OS CSPRNG.
func Open(index int) (*Source, error) {
	if index < 0 || index > 9 {
		return nil, errors.New("legacy Quantis PCI board index must be 0..9")
	}

	paths, err := devicePaths()
	if err != nil {
		return nil, fmt.Errorf("discover legacy Quantis PCI: %w", err)
	}

	if index >= len(paths) {
		return nil, fmt.Errorf("legacy Quantis PCI board %d unavailable (%d legacy 179a:0001 interfaces); install the matching legacy Windows driver; no OS fallback", index, len(paths))
	}

	path, err := windows.UTF16PtrFromString(paths[index])
	if err != nil {
		return nil, err
	}

	// Use the vendor's read/write, exclusive access, but require overlapped I/O.
	// There is deliberately no synchronous compatibility fallback.
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, fmt.Errorf("open Quantis PCI board %d: %w", index, err)
	}

	return newSource(handle)
}

func devicePaths() ([]string, error) {
	devices, err := windows.SetupDiGetClassDevsEx(&interfaceGUID, "", 0,
		windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return nil, err
	}
	defer devices.Close()

	var paths []string

	for index := 0; ; index++ {
		device, err := devices.EnumDeviceInfo(index)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			break
		}

		if err != nil {
			return nil, err
		}

		instance, err := devices.DeviceInstanceID(device)
		if err != nil {
			return nil, err
		}

		if !legacyInstance(instance) {
			continue
		}

		interfaces, err := windows.CM_Get_Device_Interface_List(instance, &interfaceGUID,
			windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
		if err != nil {
			return nil, err
		}

		paths = append(paths, interfaces...)
	}

	slices.Sort(paths)

	return slices.Compact(paths), nil
}

func legacyInstance(instance string) bool {
	return strings.HasPrefix(strings.ToUpper(instance), `PCI\VEN_179A&DEV_0001&`)
}
