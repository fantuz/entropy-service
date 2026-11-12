package usbtransport

import (
	"bytes"
	"testing"
)

func chaosDescriptors() ([]byte, []byte) {
	device := []byte{18, 1, 0, 2, 0, 0, 0, 64, 0x50, 0x1d, 0xc6, 0x60, 0, 1, 0, 0, 0, 1}
	config := []byte{
		9, 2, 39, 0, 1, 1, 0, 0x80, 50, 9, 4, 0, 0, 3, 0xff, 0, 0, 0,
		7, 5, 0x85, 2, 64, 0, 0, 7, 5, 0x86, 2, 64, 0, 0, 7, 5, 0x87, 2, 64, 0, 0,
	}

	return device, config
}

func TestChaosKeyDescriptorIdentityAndCookedEndpoint(t *testing.T) {
	device, config := chaosDescriptors()
	if err := ChaosKey.ValidateDevice(device); err != nil {
		t.Fatal(err)
	}

	if packet, err := ChaosKey.PacketSize(config); packet != 64 || err != nil {
		t.Fatal(packet, err)
	}

	if err := Quantis.ValidateDevice(device); err == nil {
		t.Fatal("ChaosKey accepted as Quantis")
	}

	for _, index := range []int{0, 1, 8, 10, 17} {
		changed := bytes.Clone(device)

		changed[index] ^= 1
		if err := ChaosKey.ValidateDevice(changed); err == nil {
			t.Fatalf("bad identity byte %d", index)
		}
	}

	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"raw only", func(c []byte) []byte { c[20] = 0x86; return c }},
		{"flash only", func(c []byte) []byte { c[20] = 0x87; return c }},
		{"wrong interface", func(c []byte) []byte { c[11] = 1; return c }},
		{"wrong alternate", func(c []byte) []byte { c[12] = 1; return c }},
		{"interrupt endpoint", func(c []byte) []byte { c[21] = 3; return c }},
		{"high speed variant", func(c []byte) []byte { c[22] = 0; c[23] = 2; return c }},
		{"duplicate cooked", func(c []byte) []byte { c[27] = 0x85; return c }},
		{"bad endpoint count", func(c []byte) []byte { c[13] = 2; return c }},
		{"truncated", func(c []byte) []byte { return c[:len(c)-1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ChaosKey.PacketSize(tc.mutate(bytes.Clone(config))); err == nil {
				t.Fatal("unsupported cooked configuration accepted")
			}
		})
	}
}

func FuzzProfiles(f *testing.F) {
	device, config := chaosDescriptors()
	f.Add(device)
	f.Add(config)
	f.Fuzz(func(_ *testing.T, data []byte) {
		for _, profile := range []Profile{Quantis, ChaosKey, ChaosKeyRaw, 255} {
			_ = profile.ValidateDevice(data)
			_, _ = profile.PacketSize(data)
		}
	})
}

func TestChaosKeyRawCaptureCannotSubstituteCookedFlashOrQuantis(t *testing.T) {
	device, config := chaosDescriptors()
	if err := ChaosKeyRaw.ValidateDevice(device); err != nil {
		t.Fatal(err)
	}

	if packet, err := ChaosKeyRaw.PacketSize(config); err != nil || packet != 64 || ChaosKeyRaw.Endpoint() != 0x86 || ChaosKey.Endpoint() != 0x85 {
		t.Fatalf("incorrect raw/cooked routing: %d %v", packet, err)
	}

	for _, endpoint := range []byte{0x85, 0x87} {
		changed := bytes.Clone(config)
		changed[27] = endpoint

		if _, err := ChaosKeyRaw.PacketSize(changed); err == nil {
			t.Fatalf("missing raw endpoint replaced by 0x%02x", endpoint)
		}
	}

	device[8], device[9], device[10], device[11] = 0xba, 0x0a, 0x02, 0x01
	if err := ChaosKeyRaw.ValidateDevice(device); err == nil {
		t.Fatal("Quantis identity accepted for ChaosKey raw capture")
	}

	if ChaosKeyRaw.SupportsPacket(512) {
		t.Fatal("raw capture accepted unsupported packet size")
	}
}
