package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
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
	fpsdigit := 1000 / float64(cfg.Refresh.Milliseconds())

	expectedbr := float64(cfg.Slice) * fpsdigit / (1 << 20)

	fmt.Print(profile.Clear())

	sampleCtx, cancelSample := context.WithTimeout(ctx, 3*time.Second)
	testdata, err := sampleEntropy(sampleCtx, cfg.Mode, refhttpurl, refwsurl, cfg.Slice)

	cancelSample()

	if err != nil {
		return fmt.Errorf("sample entropy: %w", err)
	}

	result := analysePayload(testdata, cfg)

	fmt.Println()
	fmt.Println(profile.Paint(Gray, "EntropyCTL Monitor"))
	fmt.Println()
	fmt.Println("*--------------------------------------------*")
	fmt.Println("|  Pre-flight diagnostics on entropy source  |")
	fmt.Println("*--------------------------------------------*")
	fmt.Printf("Framerate            : %.2f FPS (1 frame every %dms)\n", float64(fpsdigit), int(cfg.Refresh.Milliseconds()))
	fmt.Println("Refresh (ms)         :", cfg.Refresh)
	fmt.Println("HTTP URL             : " + profile.Paint(Cyan, refhttpurl))
	fmt.Println("WS URL               : " + profile.Paint(Cyan, refwsurl))
	fmt.Println("----------------------------------------------")
	fmt.Println("Program mode         :", cfg.Mode)

	if cfg.WriteOut {
		fmt.Println("Write-out to /dev    : " + profile.Paint(Green, "enabled"))
	} else {
		fmt.Println("Write-out to /dev    : " + profile.Paint(Red, "disabled"))
	}

	fmt.Println("Debug                :", cfg.ShowDebug)
	fmt.Println("----------------------------------------------")
	fmt.Println("Show Dashboard       :", cfg.ShowDashboard)
	fmt.Println("Show 64x64 Matrix    :", cfg.ShowMatrix)
	fmt.Println("Show Graph           :", cfg.ShowGraph)
	fmt.Println("Data Preview         :", cfg.ShowPreview)
	fmt.Println("Waterfall / mapping  :", cfg.ShowWaterfall, cfg.WaterfallMode)
	fmt.Println("Terminal colors      :", cfg.Color)
	fmt.Println("----------------------------------------------")

	if int(cfg.Slice) == len(testdata) {
		fmt.Println("Expected Slice       :", profile.Paint(Green, strconv.FormatInt(cfg.Slice, 10)))
		fmt.Println("Fetched Size         :", profile.Paint(Green, strconv.Itoa(len(testdata))))
	} else {
		fmt.Println("Expected Slice       :", profile.Paint(Red, strconv.FormatInt(cfg.Slice, 10)))
		fmt.Println("Fetched Size         :", profile.Paint(Red, strconv.Itoa(len(testdata))))
	}

	fmt.Println("----------------------------------------------")
	fmt.Printf("Requested rate       : %s (payload target)\n", profile.Paint(Green, fmt.Sprintf("%.3f MiB/s", expectedbr)))
	fmt.Printf("Analysis prefix cap  : %d bytes (0 = full frame)\n", cfg.AnalysisBytes)
	fmt.Printf("Analysis interval    : %s (0 = every frame)\n", cfg.AnalysisInterval)
	fmt.Printf("Display interval     : %s\n", cfg.DisplayInterval)
	fmt.Printf("Pre-flight analysed  : %d / %d bytes\n", result.N, len(testdata))

	if result.N > 0 {
		fmt.Println("Link                 : data received")
	} else {
		fmt.Println("Link                 : no data received")
	}

	fmt.Println("----------------------------------------------")

	tests.PrintDiagnosticsWithColor(os.Stdout, &result, cfg.ShannonThreshold, profile.Color != terminal.Monochrome)

	if !waitContext(ctx, 3*time.Second) {
		return ctx.Err()
	}

	return nil
}
