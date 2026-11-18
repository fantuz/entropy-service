package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestMacOSSeedSourceDeadlineAndClose(t *testing.T) {
	reader, source, err := openEntropySource(DefaultCSPRNG, false)
	if err != nil || source != "macos-csprng" {
		t.Fatalf("source: %s, %v", source, err)
	}
	defer reader.Close()

	sourceReader, ok := reader.(*darwinSeedSource)
	if !ok {
		t.Fatalf("unexpected OS source type %T", reader)
	}

	if err := sourceReader.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	var bytes [64]byte
	if n, err := reader.Read(bytes[:]); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired read: %d, %v", n, err)
	}

	if err := sourceReader.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}

	if n, err := reader.Read(bytes[:]); n != len(bytes) || err != nil {
		t.Fatalf("OS read: %d, %v", n, err)
	}

	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := reader.Read(bytes[:]); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed source: %v", err)
	}

	for _, path := range []string{"/dev/urandom", "quantis-usb:0", "chaoskey-usb:0"} {
		if _, _, err := openEntropySource(path, false); err == nil {
			t.Fatalf("silently substituted OS source for %q", path)
		}
	}
}
