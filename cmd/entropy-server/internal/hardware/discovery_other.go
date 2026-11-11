//go:build !linux && !windows

package hardware

import (
	"errors"
	"runtime"
)

func Discover() (Inventory, error) {
	return Inventory{SchemaVersion: 1, Platform: runtime.GOOS, Devices: []Device{}},
		errors.New("hardware discovery is supported on Linux and Windows")
}
