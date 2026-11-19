package lab

import (
	"runtime"
	"slices"
	"testing"
)

func TestLABSupportedKindsAcrossPlatforms(t *testing.T) {
	for _, goos := range []string{linuxOS, "windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			kinds := supportedKinds(goos)
			for _, kind := range []string{
				sourceHTTP, sourceHTTPS, sourceWS, sourceEntropyHTTP,
				sourceEntropyWS, sourceTCP, sourceUDP, sourceWebpage, sourceRTSP, sourceONVIF,
			} {
				if !slices.Contains(kinds, kind) {
					t.Errorf("shared source %s unavailable on %s", kind, goos)
				}
			}

			if slices.Contains(kinds, sourceDevice) != (goos == linuxOS) ||
				slices.Contains(kinds, sourceUnix) != (goos != windowsOS) {
				t.Fatalf("incorrect device/socket platform support: %v", kinds)
			}
		})
	}
}

func TestLABRejectsUnsupportedDeviceBeforeAcquisition(t *testing.T) {
	if runtime.GOOS == linuxOS {
		t.Skip("direct device registration is supported on Linux")
	}

	if _, err := New([]Config{{ID: "device", Kind: sourceDevice, Endpoint: "/dev/random"}}); err == nil {
		t.Fatal("accepted a direct device registration that cannot be read on this OS")
	}
}
