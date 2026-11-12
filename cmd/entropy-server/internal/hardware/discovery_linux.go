package hardware

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func Discover() (Inventory, error) { return discoverLinux("/sys", "/dev") }

const linuxUSBBus = "usb"

func discoverLinux(sysRoot, devRoot string) (Inventory, error) {
	result := Inventory{SchemaVersion: 1, Platform: "linux", Devices: []Device{}}

	for _, bus := range []string{"pci", linuxUSBBus} {
		entries, err := os.ReadDir(filepath.Join(sysRoot, "bus", bus, "devices"))
		if errors.Is(err, os.ErrNotExist) {
			result.Warnings = append(result.Warnings, bus+" sysfs directory unavailable")
			continue
		}

		if err != nil {
			return result, fmt.Errorf("enumerate %s devices: %w", bus, err)
		}

		for _, entry := range entries {
			path := filepath.Join(sysRoot, "bus", bus, "devices", entry.Name())

			device, ok := linuxDevice(path, bus, sysRoot, devRoot)
			if !ok {
				continue
			}

			if bus == "pci" {
				device.Paths = linuxDeviceNodes(sysRoot, devRoot, path)
				if len(device.Paths) > 0 {
					device.State = "device_node_present"
					device.Source = device.Paths[0]
					device.ProbeAdapter = true
				}
			}

			result.Devices = append(result.Devices, device)
		}
	}

	slices.SortFunc(result.Devices, func(a, b Device) int { return strings.Compare(a.ID, b.ID) })
	assignUSBSelectors(result.Devices, false)

	return result, nil
}

func linuxDevice(path, bus, sysRoot, devRoot string) (Device, bool) {
	vendorName, productName := "vendor", "device"
	if bus == linuxUSBBus {
		vendorName, productName = "idVendor", "idProduct"
	}

	vendor := strings.ToLower(strings.TrimPrefix(readAttribute(path, vendorName), "0x"))
	product := strings.ToLower(strings.TrimPrefix(readAttribute(path, productName), "0x"))

	family, model, known := Identify(vendor, product)
	if !known || (bus == linuxUSBBus) != isUSBFamily(family) {
		return Device{}, false
	}

	device := Device{
		ID: filepath.Base(path), Family: family, Model: model,
		VendorID: vendor, ProductID: product, State: "no_driver", Paths: []string{},
	}
	if target, err := os.Readlink(filepath.Join(path, "driver")); err == nil {
		device.Driver = filepath.Base(target)
		device.DriverVersion = readAttribute(filepath.Join(path, "driver", "module"), "version")
		device.State = "driver_present_no_device_node"
	}

	if bus == linuxUSBBus {
		// Driver ownership belongs to interface 0, not the USB core device.
		if target, err := os.Readlink(path + ":1.0/driver"); err == nil {
			device.Driver = filepath.Base(target)
		}

		device.Serial = readAttribute(path, "serial")
		busNumber, busErr := strconv.Atoi(readAttribute(path, "busnum"))

		address, addressErr := strconv.Atoi(readAttribute(path, "devnum"))
		if busErr == nil && addressErr == nil && busNumber > 0 && busNumber <= 999 && address > 0 && address <= 999 {
			device.Paths = []string{filepath.Join(devRoot, "bus", "usb", fmt.Sprintf("%03d", busNumber), fmt.Sprintf("%03d", address))}
		}

		device.State = "usbfs_path_reported"

		device.Notes = []string{"USB device nodes are not entropy byte streams; a control/bulk transport is required"}
		if family == ChaosKey {
			device.KernelPaths = linuxCharacterNodes(sysRoot, devRoot, path, "chaoskey*")

			device.Notes = append(device.Notes, "Cooked endpoint 0x85 only; raw 0x86 and flash 0x87 are not seed sources", "The kernel /dev/chaoskeyN interface requires a poll/nonblocking deadline contract; device existence alone does not establish it")
			if device.Driver != "" {
				device.Notes = append(device.Notes, "USB interface is owned by "+device.Driver+"; direct USB never detaches a kernel driver automatically")
			}
		}
	}

	return device, true
}

func readAttribute(path, name string) string {
	data, err := os.ReadFile(filepath.Join(path, name))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

func linuxDeviceNodes(sysRoot, devRoot, path string) []string {
	return linuxCharacterNodes(sysRoot, devRoot, path, "qrandom*")
}

func linuxCharacterNodes(sysRoot, devRoot, path, pattern string) []string {
	paths := []string{}

	realDevice, err := filepath.EvalSymlinks(path)
	if err != nil {
		return paths
	}

	candidates, _ := filepath.Glob(filepath.Join(sysRoot, "class", "*", pattern))
	for _, candidate := range candidates {
		realNode, err := filepath.EvalSymlinks(filepath.Join(candidate, "device"))
		if err != nil || (realNode != realDevice && !strings.HasPrefix(realNode, realDevice+string(filepath.Separator))) {
			continue
		}

		devicePath := filepath.Join(devRoot, filepath.Base(candidate))
		if info, err := os.Stat(devicePath); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			paths = append(paths, devicePath)
		}
	}

	slices.Sort(paths)

	return slices.Compact(paths)
}
