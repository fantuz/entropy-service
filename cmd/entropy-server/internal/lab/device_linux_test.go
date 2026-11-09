package lab

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDeviceFileAndRandom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []string{path, "/dev/urandom"} {
		registry := testRegistry(t, Config{ID: "device", Kind: "device", Endpoint: endpoint})

		stream, err := registry.Open(t.Context(), "device", 32, 20)
		if err != nil {
			t.Fatal(err)
		}

		data, err := stream.Read(t.Context(), 32)
		_ = stream.Close()

		if endpoint == path {
			if !errors.Is(err, io.EOF) || !bytes.Equal(data, []byte("fixture")) {
				t.Fatalf("file read failed: %q %v", data, err)
			}
		} else if err != nil || len(data) != 32 {
			t.Fatalf("device read failed: %v", err)
		}
	}
}

func TestDeviceDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	writer, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	registry := testRegistry(t, Config{ID: "fifo", Kind: "device", Endpoint: path, TimeoutMs: 100})

	stream, err := registry.Open(t.Context(), "fifo", 32, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	started := time.Now()

	if _, err := stream.Read(t.Context(), 32); err == nil {
		t.Fatal("empty FIFO read ignored timeout")
	}

	if time.Since(started) > time.Second {
		t.Fatal("device read did not stop at its deadline")
	}
}
