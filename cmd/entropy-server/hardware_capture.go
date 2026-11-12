package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/chaoskey"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/hardware"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/usbtransport"
	"github.com/fantuz/entropy-service/internal/privatefile"
)

const (
	captureCooked = "cooked"
	captureRaw    = "raw"
)

type captureReport struct {
	SchemaVersion      int              `json:"schema_version"`
	Source             string           `json:"source"`
	ActualSource       string           `json:"actual_source,omitempty"`
	Device             *hardware.Device `json:"device,omitempty"`
	Mode               string           `json:"mode"`
	Endpoint           string           `json:"endpoint"`
	SampleFormat       string           `json:"sample_format"`
	FirmwareVersion    *string          `json:"firmware_version"`
	Platform           string           `json:"platform"`
	Architecture       string           `json:"architecture"`
	CapturedAtUTC      string           `json:"captured_at_utc,omitempty"`
	BytesRequested     int              `json:"bytes_requested"`
	BytesCaptured      int              `json:"bytes_captured"`
	ReadElapsedMS      *float64         `json:"read_elapsed_ms"`
	ElapsedMS          float64          `json:"elapsed_ms"`
	ReadMiBPerSecond   *float64         `json:"read_mib_per_second"`
	OutputFile         string           `json:"output_file"`
	SHA256             string           `json:"sha256,omitempty"`
	CaptureOK          bool             `json:"capture_ok"`
	HealthAvailable    bool             `json:"health_available"`
	CancellationTested bool             `json:"cancellation_tested"`
	QualityAssessed    bool             `json:"quality_assessed"`
	Timeout            string           `json:"timeout"`
	DiscoveryWarnings  []string         `json:"discovery_warnings,omitempty"`
	Error              string           `json:"error,omitempty"`
}

func openChaosKeyCapture(path string, raw bool) (io.ReadCloser, string, error) {
	if _, selected, err := chaoskey.ParseSelector(path); !selected || err != nil {
		return nil, "", errors.New("capture requires an explicit chaoskey-usb:N selector")
	}

	source, actual, _, err := openHardwareUSB(path, hardware.ChaosKey, chaoskey.Available,
		func(devicePath string) (io.ReadCloser, error) { return chaoskey.OpenCapture(devicePath, raw) })

	return source, actual, err
}

func (command hardwareCommand) runCapture(ctx context.Context, args []string, output, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("entropy-server hardware capture", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	device := flags.String("device", "", "Explicit chaoskey-usb:N source from hardware list; no fallback")
	mode := flags.String("mode", captureCooked, "Endpoint: cooked (0x85, default) or raw (0x86, diagnostic capture only)")
	path := flags.String("out", "", "New regular binary file; existing paths are never overwritten")
	size := flags.Int("bytes", 1<<20, "Bytes to capture (64..8388608, a multiple of 64)")
	timeout := flags.Duration("timeout", 10*time.Second, "Discovery/open/read budget (1ms..5m); native OS calls and file writes can outlast it")
	jsonOutput := flags.Bool("json", false, "Write metadata to stdout; sample bytes go only to -out")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	_, selected, selectorErr := chaoskey.ParseSelector(*device)
	if flags.NArg() != 0 || !selected || selectorErr != nil || (*mode != captureCooked && *mode != captureRaw) || *path == "" || *path == "-" || strings.TrimSpace(*path) != *path || *size < 64 || *size > 8<<20 || *size%64 != 0 || *timeout < time.Millisecond || *timeout > 5*time.Minute {
		fmt.Fprintln(diagnostics, "capture requires -device=chaoskey-usb:N, -out=NEW_FILE, -mode=cooked|raw, -bytes=64..8388608 (multiple of 64) and -timeout=1ms..5m")

		return 2
	}

	report := command.capture(ctx, *device, *mode, *path, *size, *timeout)
	if err := writeCaptureReport(output, report, *jsonOutput); err != nil {
		fmt.Fprintf(diagnostics, "write capture report: %v\n", err)

		return 1
	}

	if !report.CaptureOK {
		return 1
	}

	return 0
}

func (command hardwareCommand) capture(parent context.Context, selector, mode, path string, size int, timeout time.Duration) (report captureReport) {
	started := time.Now()

	profile := usbtransport.ChaosKey
	if mode == captureRaw {
		profile = usbtransport.ChaosKeyRaw
	}

	report = captureReport{
		SchemaVersion: 1, Source: selector, Mode: mode, Endpoint: fmt.Sprintf("0x%02x", profile.Endpoint()),
		SampleFormat: "unchanged USB bytes; no ADC decoding or bit extraction", Platform: runtime.GOOS, Architecture: runtime.GOARCH,
		OutputFile: path, BytesRequested: size, Timeout: timeout.String(),
	}
	defer func() { report.ElapsedMS = float64(time.Since(started)) / float64(time.Millisecond) }()

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}

		report.Error = fmt.Sprintf("capture output must be a new file: %v", err)

		return report
	}

	if err := captureContextError(ctx); err != nil {
		report.Error = err.Error()

		return report
	}

	inventory, err := command.discover()
	if err != nil {
		report.DiscoveryWarnings = append(report.DiscoveryWarnings, err.Error())
	} else {
		report.DiscoveryWarnings = inventory.Warnings
		for _, device := range inventory.Devices {
			if device.Source == selector && device.Family == hardware.ChaosKey {
				report.Device = &device
				break
			}
		}
	}

	if err := captureContextError(ctx); err != nil {
		report.Error = err.Error()

		return report
	}

	source, actual, err := command.openCapture(selector, mode == captureRaw)
	if err != nil {
		report.Error = err.Error()

		return report
	}

	report.ActualSource = actual

	buffer, elapsed, err := readHardwareCapture(ctx, source, actual, selector, size, timeout)
	defer clear(buffer)

	if elapsed != nil {
		readMS := float64(*elapsed) / float64(time.Millisecond)
		report.ReadElapsedMS = &readMS
	}

	if err != nil {
		report.Error = err.Error()

		return report
	}

	if err := captureContextError(ctx); err != nil {
		report.Error = err.Error()

		return report
	}

	if err := writeCaptureFile(path, buffer); err != nil {
		report.Error = err.Error()

		return report
	}

	digest := sha256.Sum256(buffer)
	report.SHA256 = hex.EncodeToString(digest[:])
	report.BytesCaptured = len(buffer)
	report.CaptureOK = true
	report.CapturedAtUTC = time.Now().UTC().Format(time.RFC3339)

	if elapsed != nil && *elapsed > 0 {
		rate := float64(len(buffer)) / elapsed.Seconds() / (1 << 20)
		report.ReadMiBPerSecond = &rate
	}

	return report
}

func captureContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}

	return nil
}

func readHardwareCapture(ctx context.Context, source io.ReadCloser, actual, requested string, size int, timeout time.Duration) (buffer []byte, elapsed *time.Duration, err error) {
	defer func() {
		err = errors.Join(err, source.Close())
		if err != nil {
			clear(buffer)
			buffer = nil
		}
	}()

	if actual != requested {
		return nil, nil, errors.New("capture refused source substitution")
	}

	deadlineSource, ok := source.(seedDeadlineSource)
	if !ok {
		return nil, nil, errors.New("capture source has no read-deadline interface")
	}

	buffer = make([]byte, size)
	input := seedInput{source: deadlineSource, reader: source, timeout: timeout}
	started := time.Now()
	_, err = input.Reader(ctx).Read(buffer)
	duration := time.Since(started)

	return buffer, &duration, err
}

func writeCaptureFile(path string, buffer []byte) error {
	file, err := privatefile.Create(path)
	if err != nil {
		return err
	}

	stat, err := file.Stat()
	if err == nil && !stat.Mode().IsRegular() {
		err = errors.New("capture output must be a regular file")
	}

	if err == nil {
		_, err = file.Write(buffer)
	}

	if err == nil {
		err = file.Sync()
	}

	err = errors.Join(err, file.Close())
	if err != nil {
		return errors.Join(err, os.Remove(path))
	}

	return nil
}

func writeCaptureReport(output io.Writer, report captureReport, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(report)
	}

	readTime := "not performed"
	if report.ReadElapsedMS != nil {
		readTime = fmt.Sprintf("%.3f ms", *report.ReadElapsedMS)
	}

	_, err := fmt.Fprintf(output, "Source: %s\nOpened: %s\nCapture: %s endpoint %s; saved %d/%d bytes; OK: %t\nOutput: %s\nSHA-256: %s\nRead: %s; total: %.3f ms\nFirmware/ADC format and hardware health: unavailable\nCancellation tested: false; entropy quality assessed: false\nDiagnostic capture never changes the server seed source.\n",
		report.Source, report.ActualSource, report.Mode, report.Endpoint, report.BytesCaptured, report.BytesRequested, report.CaptureOK,
		report.OutputFile, report.SHA256, readTime, report.ElapsedMS)
	if report.Error != "" {
		_, writeErr := fmt.Fprintf(output, "Error: %s\n", report.Error)
		err = errors.Join(err, writeErr)
	}

	return err
}
