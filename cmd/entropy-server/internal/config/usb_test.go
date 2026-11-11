package config

import (
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/quantisusb"
)

func TestQuantisUSBExplicitPlatformSelection(t *testing.T) {
	for _, require := range []bool{false, true} {
		cfg := Config{DevicePath: "quantis-usb:0", RequireDevice: require}
		for _, goos := range []string{"linux", "windows"} {
			if err := cfg.validatePlatform(goos); (err == nil) != quantisusb.Available {
				t.Fatalf("USB selection on %s: %v", goos, err)
			}
		}

		if err := cfg.validatePlatform("darwin"); err == nil {
			t.Fatal("unsupported USB platform accepted")
		}
	}

	for _, source := range []string{"quantis-usb:-1", "quantis-usb:127", "quantis-usb:01"} {
		if err := (&Config{DevicePath: source}).validatePlatform("linux"); err == nil {
			t.Fatalf("invalid USB selector accepted: %s", source)
		}
	}
}
