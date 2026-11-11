package hardware

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxDiscoveryDistinguishesMissingDriverAndDeviceNode(t *testing.T) {
	root := t.TempDir()
	sysRoot, devRoot := filepath.Join(root, "sys"), filepath.Join(root, "dev")
	pci := filepath.Join(sysRoot, "bus", "pci", "devices")

	usb := filepath.Join(sysRoot, "bus", "usb", "devices")
	for _, path := range []string{pci, usb, devRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	write := func(dir, name, value string) {
		t.Helper()

		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	legacy := filepath.Join(pci, "0000:01:00.0")
	write(legacy, "vendor", "0x179a")
	write(legacy, "device", "0x0001")

	modern := filepath.Join(pci, "0000:02:00.0")
	write(modern, "vendor", "0x1e89")
	write(modern, "device", "0x0003")

	unrelated := filepath.Join(pci, "0000:03:00.0")
	write(unrelated, "vendor", "0x179a")
	write(unrelated, "device", "0x9999")

	usbDevice := filepath.Join(usb, "1-2")
	write(usbDevice, "idVendor", "0aba")
	write(usbDevice, "idProduct", "0102")
	write(usbDevice, "serial", "CaseSensitiveSerial")
	write(usbDevice, "busnum", "1")
	write(usbDevice, "devnum", "3")

	driver := filepath.Join(root, "drivers", "quantis_pci")
	if err := os.MkdirAll(driver, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(driver, filepath.Join(legacy, "driver")); err != nil {
		t.Fatal(err)
	}

	class := filepath.Join(sysRoot, "class", "qrandom", "qrandom0")
	if err := os.MkdirAll(class, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(legacy, filepath.Join(class, "device")); err != nil {
		t.Fatal(err)
	}

	first, err := discoverLinux(sysRoot, devRoot)
	if err != nil || len(first.Devices) != 3 {
		t.Fatalf("inventory: %+v, %v", first, err)
	}

	if first.Devices[0].State != "driver_present_no_device_node" || first.Devices[0].Source != "" || first.Devices[1].State != "no_driver" {
		t.Fatalf("binding/node states confused: %+v", first.Devices)
	}

	if first.Devices[2].Serial != "CaseSensitiveSerial" || first.Devices[2].ProbeAdapter || first.Devices[2].Source != "" {
		t.Fatalf("USB metadata treated as a byte source: %+v", first.Devices[2])
	}

	if err := os.Symlink("/dev/null", filepath.Join(devRoot, "qrandom0")); err != nil {
		t.Fatal(err)
	}

	second, err := discoverLinux(sysRoot, devRoot)
	if err != nil || second.Devices[0].Source != filepath.Join(devRoot, "qrandom0") || !second.Devices[0].ProbeAdapter {
		t.Fatalf("bound node not mapped to its PCI device: %+v, %v", second, err)
	}
}

func TestLinuxStatusFailureAndModelSpecificHealth(t *testing.T) {
	for _, tc := range []struct {
		family string
		mask   uint32
		status uint32
		ok     bool
	}{
		{LegacyPCI, 1, 1, true},
		{LegacyPCI, 3, 1, false},
		{LegacyPCI, 0xfff, 0xfff, false},
		{ChipPCIe, 0xfff, 0xfff, true},
		{ChipPCIe, 0, 0, false},
	} {
		status, err := linuxStatus(7, tc.family, func(fd int, request uint) (uint32, error) {
			if fd != 7 {
				t.Fatalf("wrong descriptor: %d", fd)
			}

			switch request {
			case 0x80047102:
				return tc.mask, nil
			case 0x80047108:
				return tc.status, nil
			case 0x80047100, 0x80047103:
				return 202, nil
			default:
				t.Fatalf("write/reset/unknown request issued: %x", request)

				return 0, nil
			}
		})
		if (err == nil) != tc.ok || status["modules_mask"] != tc.mask {
			t.Fatalf("health for %+v: %+v, %v", tc, status, err)
		}
	}

	want := errors.New("unplugged")
	if _, err := linuxStatus(1, LegacyPCI, func(int, uint) (uint32, error) { return 0, want }); !errors.Is(err, want) {
		t.Fatalf("status failure swallowed: %v", err)
	}
}
