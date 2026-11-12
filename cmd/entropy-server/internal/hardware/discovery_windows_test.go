package hardware

import "testing"

func TestWindowsInventoryIdentitiesAndLegacyOnlySelection(t *testing.T) {
	for _, tc := range []struct {
		id     string
		family string
	}{
		{`PCI\VEN_179A&DEV_0001&SUBSYS_00000000\1`, LegacyPCI},
		{`PCI\VEN_1E89&DEV_0003&SUBSYS_00000000\2`, ChipPCIe},
		{`USB\VID_0ABA&PID_0102\Serial`, USB},
		{`USB\VID_1D50&PID_60C6\ChaosSerial`, ChaosKey},
	} {
		device, known := windowsIdentity(tc.id)
		if !known || device.Family != tc.family || device.State != "no_driver" {
			t.Fatalf("identity %+v: %+v, %t", tc, device, known)
		}
	}

	for _, id := range []string{`PCI\VEN_179A&DEV_00010&`, `USB\VID_179A&PID_0001\1`, `PCI\VEN_0ABA&DEV_0102\1`} {
		if _, known := windowsIdentity(id); known {
			t.Fatalf("unrelated device accepted: %s", id)
		}
	}

	devices := []Device{
		{Family: LegacyPCI, Paths: []string{"z"}},
		{Family: LegacyPCI, Paths: []string{"a"}},
		{Family: ChipPCIe, Paths: []string{"x"}},
		{Family: USB, Paths: []string{"y"}},
	}
	assignLegacySelectors(devices)

	if devices[0].Source != "quantis-pci:1" || devices[1].Source != "quantis-pci:0" || devices[2].Source != "" || devices[3].ProbeAdapter {
		t.Fatalf("selection confused models or interface ordering: %+v", devices)
	}
}

func TestWindowsDiscoveryWithoutOpeningHardware(t *testing.T) {
	inventory, err := Discover()
	if err != nil || inventory.Platform != "windows" || inventory.Devices == nil {
		t.Fatalf("native discovery: %+v, %v", inventory, err)
	}
}
