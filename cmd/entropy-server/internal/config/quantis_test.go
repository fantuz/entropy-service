package config

import "testing"

func TestQuantisPCISelectorsAndPlatforms(t *testing.T) {
	for _, tc := range []struct {
		source   string
		index    int
		selected bool
		valid    bool
	}{
		{"quantis-pci:0", 0, true, true},
		{"quantis-pci:9", 9, true, true},
		{"quantis-pci", 0, true, false},
		{"quantis-pci:", 0, true, false},
		{"quantis-pci:-1", 0, true, false},
		{"quantis-pci:10", 0, true, false},
		{"quantis-pci:+1", 0, true, false},
		{"quantis-pci:01", 0, true, false},
		{"quantis-pci:0/../../file", 0, true, false},
		{WindowsCSPRNG, 0, false, true},
		{"/dev/qrandom0", 0, false, true},
	} {
		index, selected, err := ParseQuantisPCI(tc.source)
		if selected != tc.selected || (err == nil) != tc.valid || (err == nil && index != tc.index) {
			t.Fatalf("selector %+v: index=%d selected=%t error=%v", tc, index, selected, err)
		}
	}

	for _, require := range []bool{false, true} {
		cfg := Config{DevicePath: "quantis-pci:0", RequireDevice: require}
		if err := cfg.validatePlatform("windows"); err != nil {
			t.Fatalf("valid Windows hardware selection rejected: %v", err)
		}

		if err := cfg.validatePlatform("linux"); err == nil {
			t.Fatal("Windows hardware selector silently treated as a Linux device path")
		}
	}
}
