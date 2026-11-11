// Package hardware inventories known Quantis devices without opening them.
package hardware

import "strings"

const (
	LegacyPCI = "quantis-pci"
	ChipPCIe  = "quantis-pcie-chip"
	USB       = "quantis-usb"
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

// Identify is intentionally a model/protocol allowlist, not all Quantis IDs.
func Identify(vendor, product string) (family, model string, known bool) {
	switch strings.ToLower(vendor + ":" + product) {
	case "179a:0001":
		return LegacyPCI, "Quantis legacy PCI interface", true
	case "1e89:0002":
		return ChipPCIe, "Quantis PCIe-40M", true
	case "1e89:0003":
		return ChipPCIe, "Quantis PCIe-240M", true
	case "0aba:0102":
		return USB, "Quantis USB (legacy protocol)", true
	default:
		return "", "", false
	}
}
