package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/device"
)

const (
	reasonDuration       = "duration"
	reasonInterrupted    = "interrupted"
	reasonTransportError = "transport_error"
	reasonStartupError   = "startup_error"
	reasonReportError    = "report_error"
	reasonWriteoutError  = "writeout_error"
)

func runConfigured(ctx context.Context, cfg *config.Config) error {
	return runConfiguredWithWriteout(ctx, cfg, func() (writeoutSink, error) { return device.Open() })
}

func runConfiguredWithWriteout(ctx context.Context, cfg *config.Config, openDevice func() (writeoutSink, error)) (runErr error) {
	if err := cfg.ValidateAnalysis(); err != nil {
		return err
	}

	if cfg.Mode != modePull && cfg.Mode != modeStream {
		return errors.New("mode must be pull or stream")
	}

	if cfg.Slice < 1 || cfg.Slice > 1<<25 {
		return errors.New("slice must be between 1 and 33554432 bytes")
	}

	if cfg.Refresh < time.Millisecond {
		return errors.New("refresh must be at least 1ms")
	}

	httpURL, wsURL, err := requestEndpoints(cfg)
	if err != nil {
		return fmt.Errorf("configure endpoint: %w", err)
	}

	report, err := openReport(cfg.ReportPath)
	if err != nil {
		return err
	}

	defer func() { runErr = errors.Join(runErr, report.close()) }()

	began := time.Now()

	endpoint := httpURL
	if cfg.Mode == modeStream {
		endpoint = wsURL
	}

	if err := report.start(cfg, endpoint); err != nil {
		return err
	}

	m := monitorState{cfg: cfg}
	defer func() { runErr = errors.Join(runErr, m.closeWriteout()) }()

	finish := func(reason string, err error) error {
		if closeErr := m.closeWriteout(); closeErr != nil {
			if err == nil {
				reason = reasonWriteoutError
			}

			err = errors.Join(err, closeErr)
		}

		return errors.Join(err, report.summary(&m, began, reason, err != nil))
	}
	if cfg.WriteOut {
		m.writeout, err = openDevice()
		if err != nil {
			m.deviceErrors++
			return finish(reasonStartupError, fmt.Errorf("enable writeout: %w", err))
		}
	}

	if !cfg.Headless {
		if err := showPreflight(ctx, cfg, httpURL, wsURL); err != nil {
			reason := reasonStartupError
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				reason, err = reasonInterrupted, nil
			}

			return finish(reason, err)
		}
	}

	m.started = time.Now()

	if err := report.write(newReportHeader("measurement_start")); err != nil {
		return finish(reasonReportError, err)
	}

	reason, err := runMonitor(ctx, &m, report, httpURL, wsURL)

	return finish(reason, err)
}
