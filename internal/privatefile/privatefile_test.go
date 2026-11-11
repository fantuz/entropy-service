package privatefile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreatePrivateAndRefuseReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.bin")

	file, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}

	want := []byte{0, 10, 13, 128, 255}
	if _, err := file.Write(want); err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Check(path); err != nil {
		t.Fatal(err)
	}

	if replacement, err := Create(path); replacement != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing private file accepted: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("private file was changed by refused creation", err)
	}

	if err := Check(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted as a private regular file")
	}
}
