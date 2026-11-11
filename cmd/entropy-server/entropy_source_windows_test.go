package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestWindowsOSSeedLifecycle(t *testing.T) {
	opened, origin, err := openEntropySource(DefaultCSPRNG, false)
	if err != nil || origin != DefaultCSPRNG {
		t.Fatalf("OS source: %q, %v", origin, err)
	}
	defer opened.Close()

	source, ok := opened.(seedDeadlineSource)
	if !ok {
		t.Fatal("Windows source does not support the shared seed lifecycle")
	}

	input := seedInput{source: source, reader: source, timeout: time.Second}
	seed := make([]byte, 64)

	if n, err := io.ReadFull(input.Reader(t.Context()), seed); err != nil || n != len(seed) {
		t.Fatalf("root seed: %d, %v", n, err)
	}

	if err := source.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	if n, err := source.Read(seed); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired deadline ignored: %d, %v", n, err)
	}

	if err := source.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if n, err := input.Reader(ctx).Read(seed); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled seed read: %d, %v", n, err)
	}

	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	if n, err := source.Read(seed); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed source remained usable: %d, %v", n, err)
	}

	if err := source.SetReadDeadline(time.Time{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed source accepted a deadline: %v", err)
	}
}

func TestWindowsNeverSubstitutesForExplicitDeviceRequests(t *testing.T) {
	for _, tc := range []struct {
		path    string
		require bool
	}{
		{"/dev/qrandom0", false},
		{DefaultCSPRNG, true},
		{"", true},
		{"quantis-pci:-1", false},
		{"quantis-pci:10", true},
	} {
		if source, origin, err := openEntropySource(tc.path, tc.require); err == nil || source != nil || origin != "" {
			t.Fatalf("unsupported request silently fell back: %+v, %q, %v", tc, origin, err)
		}
	}
}

func TestWindowsHardwareFailureDoesNotSubstituteOSSource(t *testing.T) {
	want := errors.New("test: legacy driver unavailable")

	for _, require := range []bool{false, true} {
		calls := 0
		opener := func(index int) (io.ReadCloser, error) {
			calls++

			if index != 0 {
				t.Fatalf("wrong board: %d", index)
			}

			return nil, want
		}

		if source, origin, err := selectWindowsEntropySource("quantis-pci:0", require, opener); !errors.Is(err, want) || source != nil || origin != "" || calls != 1 {
			t.Fatalf("explicit hardware failure silently fell back: source=%v origin=%q error=%v", source, origin, err)
		}
	}
}

func TestWindowsHardwareSelectionReportsHardwareIdentity(t *testing.T) {
	fixture := &windowsSeedSource{}
	defer fixture.Close()

	source, origin, err := selectWindowsEntropySource("quantis-pci:2", true, func(index int) (io.ReadCloser, error) {
		if index != 2 {
			t.Fatalf("wrong board: %d", index)
		}

		return fixture, nil
	})
	if err != nil || source != fixture || origin != "quantis-pci:2" {
		t.Fatalf("hardware selection identity changed: source=%v origin=%q error=%v", source, origin, err)
	}
}
