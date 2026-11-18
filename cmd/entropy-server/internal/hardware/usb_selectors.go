//go:build linux || windows

package hardware

import (
	"slices"
	"strconv"
	"strings"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/chaoskey"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/quantisusb"
)

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
