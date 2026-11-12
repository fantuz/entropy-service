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

func TestChaosKeySelectionNeverFallsBack(t *testing.T) {
	for _, require := range []bool{false, true} {
		for _, selector := range []string{"chaoskey-usb:127", "chaoskey-usb:-1", "chaoskey-usb:0/device"} {
			opened, actual, err := openEntropySource(selector, require)
			if err == nil || opened != nil || actual != "" {
				t.Fatalf("ChaosKey fell back: %s %v %q %v", selector, opened, actual, err)
			}
		}
	}
}
