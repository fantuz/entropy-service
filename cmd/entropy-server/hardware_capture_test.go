package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/hardware"
	"github.com/fantuz/entropy-service/internal/privatefile"
)

func TestHardwareCaptureDefaultsCookedAndPreservesExplicitRawBytes(t *testing.T) {
	for _, raw := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "capture.bin")
		want := bytes.Repeat([]byte{0, 0xff, 0x80, 0x01}, 16)
		source := &probeFixture{reader: bytes.NewReader(want)}
		command := hardwareCommand{
			discover: func() (hardware.Inventory, error) {
				return hardware.Inventory{Devices: []hardware.Device{{Family: hardware.ChaosKey, Source: "chaoskey-usb:0", Driver: "fixture"}}}, nil
			},
			openCapture: func(selector string, requestedRaw bool) (io.ReadCloser, string, error) {
				if requestedRaw != raw {
					t.Fatalf("raw capture selection = %t, want %t", requestedRaw, raw)
				}

				return source, selector, nil
			},
		}

		args := []string{"capture", "-device=chaoskey-usb:0", "-bytes=64", "-out=" + path, "-json"}
		mode, endpoint := captureCooked, "0x85"

		if raw {
			args = append(args, "-mode=raw")
			mode, endpoint = captureRaw, "0x86"
		}

		var output, diagnostics bytes.Buffer
		if code := command.run(t.Context(), args, &output, &diagnostics); code != 0 {
			t.Fatalf("capture failed: %d %s %s", code, &output, &diagnostics)
		}

		var report captureReport
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("capture changed USB bytes: %x %v", got, err)
		}

		digest := sha256.Sum256(want)
		if !source.closed || !report.CaptureOK || report.BytesCaptured != 64 || report.Mode != mode || report.Endpoint != endpoint || report.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("wrong capture identity/result: %+v", report)
		}

		if report.Device == nil || report.Device.Driver != "fixture" || report.FirmwareVersion != nil || report.HealthAvailable || report.CancellationTested || report.QualityAssessed || bytes.Contains(output.Bytes(), want) {
			t.Fatalf("metadata omitted, assurance overstated or sample logged: %+v", report)
		}

		if report.ReadMiBPerSecond == nil || *report.ReadMiBPerSecond <= 0 || report.ReadElapsedMS == nil || *report.ReadElapsedMS <= 0 || report.ElapsedMS < *report.ReadElapsedMS {
			t.Fatalf("invalid read timing: %+v", report)
		}

		if err := privatefile.Check(path); err != nil {
			t.Fatalf("capture privacy policy: %v", err)
		}
	}
}

func TestHardwareCaptureInvalidOptionsNeverDiscoverOrOpen(t *testing.T) {
	command := hardwareCommand{
		discover: func() (hardware.Inventory, error) {
			t.Fatal("invalid capture invoked discovery")

			return hardware.Inventory{}, nil
		},
		openCapture: func(string, bool) (io.ReadCloser, string, error) {
			t.Fatal("invalid capture opened hardware")

			return nil, "", nil
		},
	}

	for _, args := range [][]string{
		{},
		{"-device=chaoskey-usb:0"},
		{"-device=windows-csprng", "-out=x"},
		{"-device=chaoskey-usb:00", "-out=x"},
		{"-device=quantis-usb:0", "-out=x"},
		{"-device=chaoskey-usb:0", "-out=-"},
		{"-device=chaoskey-usb:0", "-out=x", "-mode=flash"},
		{"-device=chaoskey-usb:0", "-out=x", "-endpoint=0x87"},
		{"-device=chaoskey-usb:0", "-out=x", "-bytes=65"},
		{"-device=chaoskey-usb:0", "-out=x", "-bytes=8388672"},
		{"-device=chaoskey-usb:0", "-out=x", "-timeout=0s"},
	} {
		if code := command.runCapture(t.Context(), args, io.Discard, io.Discard); code != 2 {
			t.Fatalf("invalid arguments %v returned %d", args, code)
		}
	}
}

type failingCaptureClose struct {
	*probeFixture
}

func (source *failingCaptureClose) Close() error {
	_ = source.probeFixture.Close()

	return errors.New("fixture close failed")
}

func TestHardwareCaptureFailuresLeaveNoOutput(t *testing.T) {
	for _, failure := range []string{"missing", "partial", "substitution", "close"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "capture.bin")
			source := &probeFixture{reader: strings.NewReader(strings.Repeat("x", 64))}
			command := hardwareCommand{discover: emptyProbeInventory, openCapture: func(selector string, _ bool) (io.ReadCloser, string, error) {
				switch failure {
				case "missing":
					return nil, "", errors.New("device unavailable")
				case "partial":
					source.reader = strings.NewReader("partial")
				case "substitution":
					return source, "os-fallback", nil
				case "close":
					return &failingCaptureClose{probeFixture: source}, selector, nil
				}

				return source, selector, nil
			}}

			report := command.capture(t.Context(), "chaoskey-usb:0", captureRaw, path, 64, time.Second)
			if report.CaptureOK || report.BytesCaptured != 0 || report.SHA256 != "" || report.ReadMiBPerSecond != nil || report.Error == "" {
				t.Fatalf("failed capture was accepted: %+v", report)
			}

			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed capture left a file: %v", err)
			}

			if failure == "substitution" && (!source.closed || source.reads.Load() != 0) {
				t.Fatal("substituted source was read or not closed")
			}
		})
	}
}

type signaledCaptureSource struct {
	seedDeadlineSource

	signal *seedReadSignal
}

func (source signaledCaptureSource) Read(p []byte) (int, error) { return source.signal.Read(p) }

func TestHardwareCaptureCancelsPendingReadWithoutExporting(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	reader, _ := deadlineSeedPipe(t, ctx)
	signal := &seedReadSignal{reader: reader, started: make(chan struct{})}
	source := signaledCaptureSource{seedDeadlineSource: reader, signal: signal}
	command := hardwareCommand{discover: emptyProbeInventory, openCapture: func(selector string, _ bool) (io.ReadCloser, string, error) {
		return source, selector, nil
	}}

	go func() {
		select {
		case <-signal.started:
			cancel()
		case <-ctx.Done():
		}
	}()

	path := filepath.Join(t.TempDir(), "capture.bin")

	report := command.capture(ctx, "chaoskey-usb:0", captureRaw, path, 64, time.Second)
	if report.CaptureOK || !strings.Contains(report.Error, context.Canceled.Error()) {
		t.Fatalf("pending capture not cancelled: %+v", report)
	}

	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled capture left output: %v", err)
	}
}

func TestHardwareCaptureNeverOverwritesExistingDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.bin")

	want := []byte("keep existing contents")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	command := hardwareCommand{openCapture: func(string, bool) (io.ReadCloser, string, error) {
		t.Fatal("existing destination opened hardware")

		return nil, "", nil
	}}
	if report := command.capture(t.Context(), "chaoskey-usb:0", captureCooked, path, 64, time.Second); report.CaptureOK || report.Error == "" {
		t.Fatalf("existing destination accepted: %+v", report)
	}

	// O_EXCL also protects a destination created after the initial path check.
	if err := writeCaptureFile(path, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive creation failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("existing destination changed: %q %v", got, err)
	}
}

func TestHardwareCaptureRejectsExistingSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.bin")
	link := filepath.Join(root, "capture.bin")

	if err := os.WriteFile(target, []byte("preserve target"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, link); err != nil {
		if windowsBuild {
			t.Skipf("Windows symlink privilege unavailable: %v", err)
		}

		t.Fatal(err)
	}

	report := (hardwareCommand{}).capture(t.Context(), "chaoskey-usb:0", captureRaw, link, 64, time.Second)
	if report.CaptureOK || report.Error == "" {
		t.Fatalf("capture followed existing symlink: %+v", report)
	}

	got, err := os.ReadFile(target)
	if err != nil || string(got) != "preserve target" {
		t.Fatalf("symlink target changed: %q %v", got, err)
	}
}

func TestHardwareCaptureExpiredBudgetNeverDiscoversOrOpens(t *testing.T) {
	parent := pendingDeadlineContext{deadline: time.Now().Add(-time.Second), done: make(chan struct{})}
	path := filepath.Join(t.TempDir(), "capture.bin")
	// Nil callbacks would panic if discovery or open ran before budget checking.
	report := (hardwareCommand{}).capture(parent, "chaoskey-usb:0", captureRaw, path, 64, time.Second)
	if report.CaptureOK || !strings.Contains(report.Error, context.DeadlineExceeded.Error()) || report.ReadElapsedMS != nil {
		t.Fatalf("expired budget reached hardware: %+v", report)
	}

	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired capture left output: %v", err)
	}
}
