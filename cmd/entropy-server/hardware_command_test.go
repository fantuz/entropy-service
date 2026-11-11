package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/hardware"
)

type probeFixture struct {
	mu       sync.Mutex
	deadline time.Time
	reader   io.Reader
	closed   bool
	reads    atomic.Int32
	status   func(time.Time) (map[string]uint32, error)
}

func (source *probeFixture) Read(p []byte) (int, error) {
	source.reads.Add(1)

	return source.reader.Read(p[:min(len(p), 7)])
}

func (source *probeFixture) Close() error {
	source.closed = true

	return nil
}

func (source *probeFixture) SetReadDeadline(deadline time.Time) error {
	source.mu.Lock()
	defer source.mu.Unlock()

	source.deadline = deadline

	return nil
}

func (source *probeFixture) ProbeStatus() (map[string]uint32, error) {
	source.mu.Lock()
	deadline := source.deadline
	source.mu.Unlock()

	if source.status == nil {
		return map[string]uint32{}, nil
	}

	return source.status(deadline)
}

func emptyProbeInventory() (hardware.Inventory, error) {
	return hardware.Inventory{SchemaVersion: 1, Platform: "fixture", Devices: []hardware.Device{}}, nil
}

func TestHardwareListAndInvalidArgumentsNeverOpenADevice(t *testing.T) {
	command := hardwareCommand{discover: emptyProbeInventory, open: func(string) (io.ReadCloser, string, error) {
		t.Fatal("list/invalid command opened a source")

		return nil, "", nil
	}}

	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"list", "-json"}, 0},
		{[]string{"probe"}, 2},
		{[]string{"probe", "-device=x", "-bytes=0"}, 2},
		{[]string{"probe", "-device=x", "-timeout=0s"}, 2},
		{[]string{"probe", "-device=x", "-bytes=1048577"}, 2},
		{[]string{"list", "extra"}, 2},
		{[]string{"unknown"}, 2},
	} {
		var output, diagnostics bytes.Buffer
		if code := command.run(t.Context(), tc.args, &output, &diagnostics); code != tc.code {
			t.Fatalf("arguments %v: code %d, output %s %s", tc.args, code, &output, &diagnostics)
		}
	}
}

func TestHardwareProbeDiscardsCompleteAndPartialSamples(t *testing.T) {
	for _, size := range []int{17, 64} {
		sample := strings.Repeat("PRIVATE_SAMPLE_", 8)[:size]
		source := &probeFixture{reader: strings.NewReader(sample)}
		command := hardwareCommand{discover: emptyProbeInventory, open: func(path string) (io.ReadCloser, string, error) { return source, path, nil }}

		var output, diagnostics bytes.Buffer

		code := command.run(t.Context(), []string{"probe", "-device=fixture", "-json"}, &output, &diagnostics)

		var report probeReport

		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}

		if strings.Contains(output.String()+diagnostics.String(), sample) || !source.closed || report.QualityAssessed || report.CancellationTested {
			t.Fatalf("sample disclosure/lifecycle/assurance failure: %+v, %s", report, &output)
		}

		if size == 64 && (code != 0 || !report.ReadOK || report.BytesAccepted != 64) {
			t.Fatalf("short reads not combined: %+v", report)
		}

		if size == 17 && (code != 1 || report.ReadOK || report.BytesAccepted != 0 || report.Error == "") {
			t.Fatalf("partial sample accepted: %+v", report)
		}
	}
}

func TestHardwareProbeNeverSubstitutesOrReadsUnhealthySource(t *testing.T) {
	want := errors.New("driver unavailable")

	command := hardwareCommand{discover: emptyProbeInventory, open: func(string) (io.ReadCloser, string, error) { return nil, "", want }}
	if report := command.probe(t.Context(), "fixture", 64, time.Second); report.Error != want.Error() || report.ActualSource != "" {
		t.Fatalf("failed hardware selection substituted a source: %+v", report)
	}

	source := &probeFixture{reader: strings.NewReader(strings.Repeat("x", 64)), status: func(time.Time) (map[string]uint32, error) {
		return map[string]uint32{"modules_mask": 3, "modules_status": 1}, errors.New("unhealthy")
	}}
	command.open = func(path string) (io.ReadCloser, string, error) { return source, path, nil }

	report := command.probe(t.Context(), "fixture", 64, time.Second)
	if report.ReadOK || !report.HealthAvailable || source.reads.Load() != 0 || report.Health["modules_status"] != 1 {
		t.Fatalf("unhealthy source read or diagnostics lost: %+v", report)
	}

	command.open = func(string) (io.ReadCloser, string, error) { return source, "os-fallback", nil }
	if report := command.probe(t.Context(), "fixture", 64, time.Second); report.ReadOK || report.Error == "" || source.reads.Load() != 0 {
		t.Fatalf("fallback source used: %+v", report)
	}
}

func TestHardwareProbeBudgetIncludesStatusAndInterruptsPendingRead(t *testing.T) {
	source := &probeFixture{reader: strings.NewReader(strings.Repeat("x", 64)), status: func(deadline time.Time) (map[string]uint32, error) {
		time.Sleep(max(0, time.Until(deadline)) + time.Millisecond)

		return map[string]uint32{}, nil
	}}

	command := hardwareCommand{discover: emptyProbeInventory, open: func(path string) (io.ReadCloser, string, error) { return source, path, nil }}
	if report := command.probe(t.Context(), "fixture", 64, 10*time.Millisecond); report.ReadOK || source.reads.Load() != 0 {
		t.Fatalf("status time did not count toward the budget: %+v", report)
	}

	reader, _ := deadlineSeedPipe(t, t.Context())
	command.open = func(path string) (io.ReadCloser, string, error) { return reader, path, nil }

	report := command.probe(t.Context(), "pipe", 64, 20*time.Millisecond)
	if report.ReadOK || !strings.Contains(report.Error, context.DeadlineExceeded.Error()) || report.CancellationTested {
		t.Fatalf("pending pipe read not canceled or assurance overclaimed: %+v", report)
	}
}

func TestHardwareProbeOSControlAndMissingSource(t *testing.T) {
	report := (hardwareCommand{discover: emptyProbeInventory, open: openProbeSource}).probe(t.Context(), DefaultCSPRNG, 64, time.Second)
	if !report.ReadOK || report.ActualSource != DefaultCSPRNG || report.SourceType != "os_csprng_control" || report.QualityAssessed {
		t.Fatalf("native OS control: %+v", report)
	}

	if source, actual, err := openProbeSource("/definitely/not/a/qrng"); err == nil || source != nil || actual != "" {
		t.Fatalf("missing source fell back: %v, %q, %v", source, actual, err)
	}
}
