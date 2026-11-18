package config

import "testing"

func TestMacOSSourcePolicy(t *testing.T) {
	if got := defaultDeviceForOS("darwin"); got != MacOSCSPRNG {
		t.Fatalf("macOS default: %s", got)
	}

	for _, tc := range []struct {
		source          string
		required, valid bool
	}{
		{MacOSCSPRNG, false, true},
		{"", false, true},
		{MacOSCSPRNG, true, false},
		{"/dev/urandom", false, false},
		{"quantis-usb:0", false, false},
		{"chaoskey-usb:0", false, false},
	} {
		cfg := Config{DevicePath: tc.source, RequireDevice: tc.required}
		if err := cfg.validatePlatform("darwin"); (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
