// Package hardware inventories known randomness-source devices without opening them.
package hardware

import (
	"slices"
	"strconv"
	"strings"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/chaoskey"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/quantisusb"
)

const (
	LegacyPCI = "quantis-pci"
	ChipPCIe  = "quantis-pcie-chip"
	USB       = "quantis-usb"
	ChaosKey  = "chaoskey"
)

// Device reports OS-observed identity and binding, not hardware certification.
// Empty metadata is unknown; it must not be interpreted as a passed check.
type Device struct {
	ID            string   `json:"id"`
	Family        string   `json:"family"`
	Model         string   `json:"model"`
	VendorID      string   `json:"vendor_id"`
	ProductID     string   `json:"product_id"`
	Driver        string   `json:"driver,omitempty"`
	DriverVersion string   `json:"driver_version,omitempty"`
	DriverPackage string   `json:"driver_package,omitempty"`
	Serial        string   `json:"serial,omitempty"`
	State         string   `json:"state"`
	Paths         []string `json:"paths"`
	KernelPaths   []string `json:"kernel_paths,omitempty"`
	Source        string   `json:"source,omitempty"`
	ProbeAdapter  bool     `json:"probe_adapter"`
	Notes         []string `json:"notes,omitempty"`
}

type Inventory struct {
	SchemaVersion int      `json:"schema_version"`
	Platform      string   `json:"platform"`
	Devices       []Device `json:"devices"`
	Warnings      []string `json:"warnings,omitempty"`
}

// Identify is intentionally a model/protocol allowlist, not generic USB/PCI recognition.
func Identify(vendor, product string) (family, model string, known bool) {
	switch strings.ToLower(vendor + ":" + product) {
	case "179a:0001":
		return LegacyPCI, "Quantis legacy PCI interface", true
	case "1e89:0002":
		return ChipPCIe, "Quantis PCIe-40M", true
	case "1e89:0003":
		return ChipPCIe, "Quantis PCIe-240M", true
	case "1d50:60c6":
		return ChaosKey, "ChaosKey hardware-noise TRNG (cooked output)", true
	case "0aba:0102":
		return USB, "Quantis USB (legacy protocol)", true
	default:
		return "", "", false
	}
}

// USB numbering follows sorted interface paths on Windows, sysfs IDs on Linux.
// It is an inventory position, not a persistent serial-number binding.
func isUSBFamily(family string) bool { return family == USB || family == ChaosKey }

func assignUSBSelectors(devices []Device, byPath bool) {
	if !quantisusb.Available {
		return
	}

	for _, family := range []string{USB, ChaosKey} {
		indices := []int{}

		for i, device := range devices {
			if device.Family == family && len(device.Paths) > 0 {
				indices = append(indices, i)
			}
		}

		slices.SortFunc(indices, func(a, b int) int {
			if byPath {
				return strings.Compare(devices[a].Paths[0], devices[b].Paths[0])
			}

			return strings.Compare(devices[a].ID, devices[b].ID)
		})

		prefix := quantisusb.Prefix
		if family == ChaosKey {
			prefix = chaoskey.Prefix
		}

		for index, i := range indices {
			if index > 126 {
				break
			}

			devices[i].Source = prefix + strconv.Itoa(index)
			devices[i].ProbeAdapter = true
		}
	}
}
