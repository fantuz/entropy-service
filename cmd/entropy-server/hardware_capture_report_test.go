package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type captureReportFailure struct{ err error }

func (w captureReportFailure) Write([]byte) (int, error) { return 0, w.err }

func TestHardwareCaptureReportFailureKeepsCompletedPrivateFile(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "capture.bin")
		want := bytes.Repeat([]byte{0xa5}, 64)
		source := &probeFixture{reader: bytes.NewReader(want)}
		command := hardwareCommand{discover: emptyProbeInventory, openCapture: func(selector string, _ bool) (io.ReadCloser, string, error) {
			return source, selector, nil
		}}

		args := []string{"-device=chaoskey-usb:0", "-bytes=64", "-out=" + path}
		if jsonOutput {
			args = append(args, "-json")
		}

		failure := errors.New("report consumer disconnected")

		var diagnostics bytes.Buffer

		code := command.runCapture(t.Context(), args, captureReportFailure{err: failure}, &diagnostics)
		if code != 1 || !strings.Contains(diagnostics.String(), "write capture report: "+failure.Error()) {
			t.Fatalf("report failure hidden: code=%d diagnostics=%s", code, &diagnostics)
		}

		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) || !source.closed {
			t.Fatalf("completed capture lost or source leaked: bytes=%d closed=%t err=%v", len(got), source.closed, err)
		}
	}
}

func TestHardwareCaptureHumanFailureReportsCauseWithoutCreatingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.bin")
	failure := errors.New("fixture device unavailable")
	command := hardwareCommand{discover: emptyProbeInventory, openCapture: func(string, bool) (io.ReadCloser, string, error) {
		return nil, "", failure
	}}

	var output bytes.Buffer

	code := command.runCapture(t.Context(), []string{"-device=chaoskey-usb:0", "-out=" + path}, &output, io.Discard)
	if code != 1 {
		t.Fatalf("failed capture exit=%d, want 1", code)
	}

	for _, want := range []string{"endpoint 0x85", "saved 0/1048576 bytes; OK: false", "Read: not performed", "Error: " + failure.Error(), "entropy quality assessed: false"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("failure report missing %q: %s", want, &output)
		}
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed capture created output: %v", err)
	}
}
