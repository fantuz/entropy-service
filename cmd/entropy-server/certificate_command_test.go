package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCertificateCommand(t *testing.T) {
	dir := t.TempDir()
	args := []string{"-cert-file=" + filepath.Join(dir, "cert.pem"), "-key-file=" + filepath.Join(dir, "key.pem")}

	var output bytes.Buffer

	if code := runCertificateCommand(args, &output); code != 0 || !strings.Contains(output.String(), "Created self-signed") {
		t.Fatalf("generation returned %d: %s", code, &output)
	}

	output.Reset()

	reuseArgs := []string{args[0], args[1], "-if-missing"}
	if code := runCertificateCommand(reuseArgs, &output); code != 0 || !strings.Contains(output.String(), "Reusing valid") {
		t.Fatalf("reuse returned %d: %s", code, &output)
	}

	output.Reset()

	if code := runCertificateCommand(args, &output); code != 1 || !strings.Contains(output.String(), "refusing to overwrite") {
		t.Fatalf("overwrite returned %d: %s", code, &output)
	}

	if strings.Contains(output.String(), "BEGIN") {
		t.Fatal("certificate command printed PEM material")
	}
}

func TestCertificateCommandHelpAndInvalidFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"-help"}, 0},
		{[]string{"-unknown"}, 2},
		{[]string{"unexpected"}, 2},
		{[]string{"-valid-for=forever"}, 2},
		{[]string{"-valid-for=0s"}, 1},
		{[]string{"-hosts=https://localhost"}, 1},
	} {
		var output bytes.Buffer
		if got := runCertificateCommand(tc.args, &output); got != tc.code || output.Len() == 0 {
			t.Fatalf("args=%v returned %d (want %d), output=%s", tc.args, got, tc.code, &output)
		}
	}
}
