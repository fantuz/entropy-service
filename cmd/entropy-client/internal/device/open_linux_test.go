package device

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectOtherDevicesFilesSymlinksAndMissingPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "existing")

	before := []byte("preserve this existing file")
	if err := os.WriteFile(file, before, 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "urandom-link")
	if err := os.Symlink("/dev/urandom", link); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(dir, "missing")
	for _, path := range []string{file, link, missing, "/dev/null"} {
		if writer, err := openDevice(path); err == nil || writer != nil {
			t.Fatalf("accepted destination %s", path)
		}
	}

	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected regular file was modified")
	}

	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing destination was created")
	}
}

func TestLinuxMixOnlyOptIn(t *testing.T) {
	if os.Getenv("ENTROPY_TEST_URANDOM") != "1" {
		t.Skip("set ENTROPY_TEST_URANDOM=1 to mix a bounded 64-byte test sample into the host pool (no entropy credit)")
	}

	writer, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	data := bytes.Repeat([]byte{0x53, 0x19, 0xa0, 0xff}, 16)

	n, err := writer.WriteContext(t.Context(), data)
	if err != nil || n != 64 {
		t.Fatalf("real /dev/urandom write: %d bytes, %v", n, err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}
