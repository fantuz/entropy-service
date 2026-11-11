package config

import (
	"fmt"
	"strconv"
	"strings"
)

// QuantisPCIPrefix selects the experimental legacy Quantis PCI Windows adapter.
const QuantisPCIPrefix = "quantis-pci:"

// ParseQuantisPCI accepts canonical, zero-based board numbers 0..9.
// This adapter's limit follows the legacy PCI header's ten-board bound, not
// the SDK's broader mixed-device numbering. Other sources are not selected.
func ParseQuantisPCI(source string) (index int, selected bool, err error) {
	if !strings.HasPrefix(source, "quantis-pci") {
		return 0, false, nil
	}

	value := strings.TrimPrefix(source, QuantisPCIPrefix)

	index, err = strconv.Atoi(value)
	if err != nil || index < 0 || index > 9 || strconv.Itoa(index) != value {
		return 0, true, fmt.Errorf("invalid Quantis PCI source %q; use -device=quantis-pci:0 (board index 0..9)", source)
	}

	return index, true, nil
}
