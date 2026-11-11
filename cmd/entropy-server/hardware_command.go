package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/hardware"
)

type hardwareCommand struct {
	discover func() (hardware.Inventory, error)
	open     func(string) (io.ReadCloser, string, error)
}

const (
	hardwareList  = "list"
	hardwareProbe = "probe"
)

type probeReport struct {
	SchemaVersion      int               `json:"schema_version"`
	Source             string            `json:"source"`
	ActualSource       string            `json:"actual_source,omitempty"`
	Device             *hardware.Device  `json:"device,omitempty"`
	SourceType         string            `json:"source_type"`
	BytesRequested     int               `json:"bytes_requested"`
	BytesAccepted      int               `json:"bytes_accepted"`
	ElapsedMS          float64           `json:"elapsed_ms"`
	Timeout            string            `json:"timeout"`
	Health             map[string]uint32 `json:"health,omitempty"`
	HealthAvailable    bool              `json:"health_available"`
	ReadOK             bool              `json:"read_ok"`
	CancellationTested bool              `json:"cancellation_tested"`
	QualityAssessed    bool              `json:"quality_assessed"`
	DeadlineLimitation string            `json:"deadline_limitation"`
	DiscoveryWarnings  []string          `json:"discovery_warnings,omitempty"`
	Error              string            `json:"error,omitempty"`
}

func runHardwareCommand(ctx context.Context, args []string, output, diagnostics io.Writer) int {
	command := hardwareCommand{discover: hardware.Discover, open: openProbeSource}

	return command.run(ctx, args, output, diagnostics)
}

func openProbeSource(path string) (io.ReadCloser, string, error) {
	require := path != DefaultCSPRNG

	cfg := config.Config{DevicePath: path, RequireDevice: require}
	if err := cfg.ValidatePlatform(); err != nil {
		return nil, "", err
	}

	// Explicit OS randomness is a useful control. All hardware/device paths
	// use strict selection; the post-open identity check also forbids fallback.
	source, actual, err := openEntropySource(path, require)
	if err != nil {
		return nil, "", err
	}

	if actual != path {
		_ = source.Close()

		return nil, "", fmt.Errorf("probe refused source substitution: requested %q, opened %q", path, actual)
	}

	return source, actual, nil
}

func (command hardwareCommand) run(ctx context.Context, args []string, output, diagnostics io.Writer) int {
	if len(args) == 0 || args[0] == "-help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(output, "Usage: entropy-server hardware list [-json]")
		fmt.Fprintln(output, "       entropy-server hardware probe -device=SOURCE [-bytes=64] [-timeout=3s] [-json]")
		fmt.Fprintln(output, "List reads OS metadata only. Probe opens one explicit source, queries supported health data and discards a sample.")

		return 0
	}

	action := args[0]
	if action != hardwareList && action != hardwareProbe {
		fmt.Fprintf(diagnostics, "unknown hardware action %q; use list or probe\n", action)

		return 2
	}

	flags := flag.NewFlagSet("entropy-server hardware "+action, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	jsonOutput := flags.Bool("json", false, "Write a structured report to stdout")

	var (
		device  *string
		bytes   *int
		timeout *time.Duration
	)

	if action == hardwareProbe {
		device = flags.String("device", "", "Explicit seed source; no fallback, automatic selection, device reset or module enable")
		bytes = flags.Int("bytes", 64, "Sample bytes to accept and discard (32..1048576); never printed")
		timeout = flags.Duration("timeout", 3*time.Second, "Complete read/health budget (1ms..5m); kernel calls depend on driver cancellation")
	}

	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	if flags.NArg() != 0 {
		fmt.Fprintln(diagnostics, "hardware commands do not accept extra positional arguments")

		return 2
	}

	if action == hardwareList {
		inventory, err := command.discover()
		if err != nil {
			fmt.Fprintf(diagnostics, "hardware discovery: %v\n", err)

			return 1
		}

		if err := writeInventory(output, inventory, *jsonOutput); err != nil {
			fmt.Fprintf(diagnostics, "write inventory: %v\n", err)

			return 1
		}

		return 0
	}

	if *device == "" || strings.TrimSpace(*device) != *device || *bytes < 32 || *bytes > 1<<20 || *timeout < time.Millisecond || *timeout > 5*time.Minute {
		fmt.Fprintln(diagnostics, "probe requires -device=SOURCE, -bytes=32..1048576 and -timeout=1ms..5m")

		return 2
	}

	report := command.probe(ctx, *device, *bytes, *timeout)
	if err := writeProbe(output, report, *jsonOutput); err != nil {
		fmt.Fprintf(diagnostics, "write probe: %v\n", err)

		return 1
	}

	if !report.ReadOK {
		return 1
	}

	return 0
}

func (command hardwareCommand) probe(parent context.Context, path string, size int, timeout time.Duration) (report probeReport) {
	started := time.Now()

	report = probeReport{
		SchemaVersion: 1, Source: path, SourceType: "explicit_device", BytesRequested: size, Timeout: timeout.String(),
		DeadlineLimitation: "Read deadlines are requested and late samples rejected; discovery/open and kernel cancellation depend on the OS/driver. A successful sample does not test cancellation or entropy quality.",
	}
	defer func() { report.ElapsedMS = float64(time.Since(started)) / float64(time.Millisecond) }()

	if path == DefaultCSPRNG {
		report.SourceType = "os_csprng_control"
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	inventory, err := command.discover()
	if err != nil {
		report.DiscoveryWarnings = append(report.DiscoveryWarnings, err.Error())
	} else {
		report.DiscoveryWarnings = inventory.Warnings
		for _, device := range inventory.Devices {
			if device.Source == path {
				report.Device = &device
				break
			}
		}
	}

	if err := ctx.Err(); err != nil {
		report.Error = err.Error()

		return report
	}

	source, actual, err := command.open(path)
	if err != nil {
		report.Error = err.Error()

		return report
	}
	defer func() {
		if err := source.Close(); err != nil {
			report.ReadOK = false
			report.Error = strings.TrimSpace(report.Error + "\n" + err.Error())
		}
	}()

	report.ActualSource = actual
	if actual != path {
		report.Error = "probe refused source substitution"

		return report
	}

	deadlineSource, ok := source.(seedDeadlineSource)
	if !ok {
		report.Error = "source has no read-deadline interface"

		return report
	}

	deadline, _ := ctx.Deadline()

	if err := ctx.Err(); err != nil {
		report.Error = err.Error()

		return report
	}

	if err := deadlineSource.SetReadDeadline(deadline); err != nil {
		report.Error = err.Error()

		return report
	}

	interrupted := make(chan struct{})

	stopInterrupt := context.AfterFunc(ctx, func() {
		defer close(interrupted)

		_ = deadlineSource.SetReadDeadline(time.Now())
	})
	defer func() {
		if !stopInterrupt() {
			<-interrupted
		}
	}()

	report.Health, err = probePlatformStatus(source)

	report.HealthAvailable = len(report.Health) > 0
	if err != nil {
		report.Error = err.Error()

		return report
	}

	input := seedInput{source: deadlineSource, reader: source, timeout: timeout}

	buffer := make([]byte, size)
	defer clear(buffer)

	report.BytesAccepted, err = input.Reader(ctx).Read(buffer)
	if err != nil {
		report.Error = err.Error()

		return report
	}

	report.ReadOK = report.BytesAccepted == size

	return report
}

func writeInventory(output io.Writer, inventory hardware.Inventory, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(inventory)
	}

	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "MODEL\tID\tDRIVER\tVERSION\tSTATE\tSOURCE")

	for _, device := range inventory.Devices {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", device.Model, device.ID, device.Driver, device.DriverVersion, device.State, device.Source)
	}

	fmt.Fprintf(writer, "%d matching device(s); OS metadata only; access and hardware operation not tested.\n", len(inventory.Devices))

	for _, warning := range inventory.Warnings {
		fmt.Fprintf(writer, "Warning: %s\n", warning)
	}

	return writer.Flush()
}

func writeProbe(output io.Writer, report probeReport, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(report)
	}

	_, err := fmt.Fprintf(output, "Source: %s\nOpened: %s\nRead OK: %t; accepted/discarded %d/%d bytes in %.3f ms\nHealth available: %t; status: %v\nCancellation tested: false; entropy quality assessed: false\n%s\n",
		report.Source, report.ActualSource, report.ReadOK, report.BytesAccepted, report.BytesRequested, report.ElapsedMS, report.HealthAvailable, report.Health, report.DeadlineLimitation)
	if report.Error != "" {
		_, writeErr := fmt.Fprintf(output, "Error: %s\n", report.Error)
		err = errors.Join(err, writeErr)
	}

	return err
}
