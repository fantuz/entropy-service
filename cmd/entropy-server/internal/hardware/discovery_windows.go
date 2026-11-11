package hardware

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var interfaceGUIDs = map[string]string{
	LegacyPCI: "{F2F5F1E5-380E-4194-8FC0-ED5AF63E93DF}",
	ChipPCIe:  "{E9A6DEC2-C135-4028-81E0-5CD341A7BA70}",
	USB:       "{6476DB04-8D6C-49F5-BCF3-E2C1FC313A18}",
}

const windowsUSBBus = "USB"

func Discover() (Inventory, error) {
	result := Inventory{SchemaVersion: 1, Platform: "windows", Devices: []Device{}}

	for _, bus := range []string{"PCI", windowsUSBBus} {
		devices, err := windowsDevices(bus)
		if err != nil {
			return result, err
		}

		result.Devices = append(result.Devices, devices...)
	}

	slices.SortFunc(result.Devices, func(a, b Device) int { return strings.Compare(a.ID, b.ID) })
	assignLegacySelectors(result.Devices)
	assignUSBSelectors(result.Devices, true)

	return result, nil
}

func windowsDevices(bus string) ([]Device, error) {
	set, err := windows.SetupDiGetClassDevsEx(nil, bus, 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return nil, fmt.Errorf("enumerate %s devices: %w", bus, err)
	}
	defer set.Close()

	devices := []Device{}

	for index := 0; ; index++ {
		info, err := set.EnumDeviceInfo(index)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return devices, nil
		}

		if err != nil {
			return nil, err
		}

		instance, err := set.DeviceInstanceID(info)
		if err != nil {
			return nil, err
		}

		device, known := windowsIdentity(instance)
		if !known {
			continue
		}

		if value, err := set.DeviceRegistryProperty(info, windows.SPDRP_SERVICE); err == nil {
			device.Driver, _ = value.(string)
		}

		if handle, err := set.OpenDevRegKey(info, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DRV, windows.KEY_QUERY_VALUE); err == nil {
			key := registry.Key(handle)
			device.DriverVersion, _, _ = key.GetStringValue("DriverVersion")
			device.DriverPackage, _, _ = key.GetStringValue("InfPath")
			_ = key.Close()
		}

		guid, err := windows.GUIDFromString(interfaceGUIDs[device.Family])
		if err != nil {
			return nil, err
		}

		paths, err := windows.CM_Get_Device_Interface_List(instance, &guid, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
		if err == nil {
			device.Paths = paths
			device.State = "interface_present"
		} else if !errors.Is(err, windows.ERROR_NO_SUCH_DEVICE_INTERFACE) {
			device.Notes = append(device.Notes, fmt.Sprintf("interface lookup: %v", err))
		}

		if len(device.Paths) == 0 && device.Driver != "" {
			device.State = "driver_present_interface_missing"
		}

		if device.Family == ChipPCIe {
			device.Notes = append(device.Notes, "no seed adapter for this model in this build")
		}

		devices = append(devices, device)
	}
}

func windowsIdentity(instance string) (Device, bool) {
	upper := strings.ToUpper(instance)

	bus, rest, ok := strings.Cut(upper, `\`)
	if !ok || (bus != "PCI" && bus != windowsUSBBus) {
		return Device{}, false
	}

	var vendor, product string

	parts, _, _ := strings.Cut(rest, `\`)
	for field := range strings.SplitSeq(parts, "&") {
		if strings.HasPrefix(field, "VEN_") || strings.HasPrefix(field, "VID_") {
			vendor = strings.ToLower(field[4:])
		}

		if strings.HasPrefix(field, "DEV_") || strings.HasPrefix(field, "PID_") {
			product = strings.ToLower(field[4:])
		}
	}

	family, model, known := Identify(vendor, product)
	if !known || (bus == windowsUSBBus) != (family == USB) {
		return Device{}, false
	}

	return Device{
		ID: instance, Family: family, Model: model, VendorID: vendor, ProductID: product,
		State: "no_driver", Paths: []string{},
	}, true
}

func assignLegacySelectors(devices []Device) {
	paths := []string{}

	for _, device := range devices {
		if device.Family == LegacyPCI {
			paths = append(paths, device.Paths...)
		}
	}

	slices.Sort(paths)
	paths = slices.Compact(paths)

	for i := range devices {
		if devices[i].Family == LegacyPCI && len(devices[i].Paths) > 0 {
			index, _ := slices.BinarySearch(paths, devices[i].Paths[0])
			if index < 10 {
				devices[i].Source = LegacyPCI + ":" + strconv.Itoa(index)
				devices[i].ProbeAdapter = true
			}
		}
	}
}
