package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/tests"
)

type Stats struct {
	TotalBytes int64
	Rate       float64
}

const (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[37m"
	White   = "\033[97m"
)

const (
	modePull   = "pull"
	modeStream = "stream"
)

func drawUI(out io.Writer, result *diag.Diagnostics, threshold float64, profile terminal.Profile) {
	fmt.Fprint(out, profile.Clear())
	fmt.Fprintln(out, profile.Paint(Gray, "EntropyCTL Monitor"))
	fmt.Fprintln(out)
	tests.PrintDiagnosticsWithColor(out, result, threshold, profile.Color != terminal.Monochrome)
}

func main() {
	log.SetPrefix("entropy-client ")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)

	stop()

	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	return runConfigured(ctx, config.ParseConfig())
}

// Interactive preflight is separate from the measured experiment.
func showPreflight(ctx context.Context, cfg *config.Config, refhttpurl, refwsurl string) error {
	profile := terminal.Detect(cfg.Color)
	fmt.Print(profile.Clear())

	sampleCtx, cancelSample := context.WithTimeout(ctx, 3*time.Second)
	testdata, err := sampleEntropy(sampleCtx, cfg.Mode, refhttpurl, refwsurl, cfg.Slice)

	cancelSample()

	if err != nil {
		return fmt.Errorf("sample entropy: %w", err)
	}

	printPreflight(os.Stdout, cfg, refhttpurl, refwsurl, testdata, profile)

	if !waitContext(ctx, 3*time.Second) {
		return ctx.Err()
	}

	return nil
}
