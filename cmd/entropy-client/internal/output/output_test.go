package output

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewPrivateFileAndNoReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.bin")

	w, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}

	data := bytes.Repeat([]byte{0, 1, 127, 255}, 30000)

	n, err := w.WriteContext(t.Context(), data)
	if err != nil || n != len(data) {
		t.Fatalf("%d: %v", n, err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, got) {
		t.Fatal("raw bytes changed", err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("output is not private", err)
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{path, link} {
		if writer, err := OpenFile(target); err == nil || writer != nil {
			t.Fatal("replaced existing output", target)
		}
	}

	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(data, got) {
		t.Fatal("rejected output was modified", err)
	}

	if _, err := w.WriteContext(t.Context(), data); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestCancellationAndSyncFailure(t *testing.T) {
	w, err := OpenFile(filepath.Join(t.TempDir(), "capture.bin"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if n, err := w.WriteContext(ctx, []byte{1}); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("%d %v", n, err)
	}
	// Simulate a descriptor failing before final fsync/close.
	if err := w.file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err == nil {
		t.Fatal("sync/close failure was hidden")
	}
}
