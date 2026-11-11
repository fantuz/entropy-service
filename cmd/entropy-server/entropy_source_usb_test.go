package main

import "testing"

func TestUSBSourceSelectionRejectsMalformedIndicesWithoutFallback(t *testing.T) {
	for _, require := range []bool{false, true} {
		for _, source := range []string{"quantis-usb:127", "quantis-usb:-1", "quantis-usb:0/device"} {
			opened, actual, err := openEntropySource(source, require)
			if err == nil || opened != nil || actual != "" {
				t.Fatalf("USB source fell back: %q %v %s %v", source, opened, actual, err)
			}
		}
	}
}
