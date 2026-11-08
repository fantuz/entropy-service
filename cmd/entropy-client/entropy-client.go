package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/tests"
)

type Stats struct {
	TotalBytes int64
	Rate       float64
	Entropy    float64
	Hist       [256]int
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

func drawUI(stats *Stats, result *diag.Diagnostics) {
	fmt.Print("\033[H\033[2J")
	fmt.Println(Gray + "EntropyCTL Monitor" + Reset)
	fmt.Println()

	// t := (*diag.RateMeter).RateMbps()
	// t := diag.NewRateMeter()
	// t.Update(int(stats.Rate))

	// fmt.Printf("Dashboard : %v\n", t)
	// fmt.Printf("Dashboard : %q\n", t)
	// fmt.Printf("Rate               : %.6f MB/s\n", stats.Rate/1024)
	// fmt.Printf("Fetched            : %d KB\n", (stats.TotalBytes/1024))
	// fmt.Printf("Meter     : %d\n", &t.Meter)
	// fmt.Printf("Rate      : %d\n", &t.Rate)
	// fmt.Printf("Fetched R : %d\n", &t.Bytes)
	// fmt.Printf("Entropy            : %.4f bits/byte\n", stats.Entropy)

	status := Green + "OK" + Reset
	if stats.Entropy < 7.999 {
		status = Red + "WARN" + Reset
	}

	fmt.Printf("Status             : %s\n", status)
	fmt.Println()

	// (*diag.RateMeter).Update(diag.RateMeter.rate, 1024)
	// t.Update(len(data))
	tests.PrintDiagnostics(result)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	canceled := ctx.Err() != nil

	stop()

	if err != nil && !canceled {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg := config.ParseConfig()
	if err := cfg.ValidateAnalysis(); err != nil {
		return err
	}

	if cfg.Mode != modePull && cfg.Mode != modeStream {
		panic("mode must be pull or stream")
	}
	// flag.Parse()
	//
	// if cfg.WriteOut {
	// 	//panic("Write Out Device issue")
	// }
	//
	// if cfg.ShowDebug {
	// 	//panic("Debug forbidden ;)")
	// }

	if cfg.Slice < 1 || cfg.Slice > 33554432 {
		panic(Red + "slice must be between 1 and 33554432 bytes" + Reset)
	}

	if cfg.Refresh < time.Millisecond {
		panic(Red + "refresh must be at least 1ms" + Reset)
	}

	refhttpurl, refwsurl, err := requestEndpoints(cfg)
	if err != nil {
		return fmt.Errorf("configure entropy endpoint: %w", err)
	}

	fpsdigit := 1000 / float64(cfg.Refresh.Milliseconds())

	expectedbr := float64(cfg.Slice) * fpsdigit / (1 << 20)

	fmt.Print("\033[H\033[2J")
	fmt.Print("\a")

	sampleCtx, cancelSample := context.WithTimeout(ctx, 3*time.Second)
	testdata, err := sampleEntropy(sampleCtx, cfg.Mode, refhttpurl, refwsurl, cfg.Slice)

	cancelSample()

	if err != nil {
		return fmt.Errorf("sample entropy: %w", err)
	}

	/*
		for i:=0; i < 10; i++ {
			result := diag.RunDiagnostics(testdata)
			if result.Pass == true {
				fmt.Printf("PASS                 : " + Green + "%v" + Reset + "\n", result.Pass)
			} else {
				fmt.Printf("PASS                 : " + Red + "%v"+ Reset + "\n", result.Pass)
			}
		}
	*/

	result := analysePayload(testdata, cfg)
	// test := diag.NewRateMeter

	fmt.Println()
	fmt.Println(Gray + "EntropyCTL Monitor" + Reset)
	fmt.Println()
	fmt.Println("*--------------------------------------------*")
	fmt.Println("|  Pre-flight diagnostics on entropy source  |")
	fmt.Println("*--------------------------------------------*")
	// fmt.Println("Framerate            : " + strconv.Itoa(fpsdigit) + " FPS (1 frame every " + strconv.Itoa(int(cfg.Refresh.Milliseconds())) + "ms)")
	fmt.Printf("Framerate            : %.2f FPS (1 frame every %dms)\n", float64(fpsdigit), int(cfg.Refresh.Milliseconds()))
	fmt.Println("Refresh (ms)         :", cfg.Refresh)
	fmt.Println("HTTP URL             : " + Cyan + refhttpurl + Reset)
	fmt.Println("WS URL               : " + Cyan + refwsurl + Reset)
	fmt.Println("----------------------------------------------")
	fmt.Println("Program mode         :", cfg.Mode)

	if cfg.WriteOut {
		fmt.Println("Write-out to /dev    : " + Green + "enabled" + Reset)
	} else {
		fmt.Println("Write-out to /dev    : " + Red + "disabled" + Reset)
	}

	fmt.Println("Debug                :", cfg.ShowDebug)
	fmt.Println("----------------------------------------------")
	fmt.Println("Show Dashboard       :", cfg.ShowDashboard)
	fmt.Println("Show 64x64 Matrix    :", cfg.ShowMatrix)
	fmt.Println("Show Graph           :", cfg.ShowGraph)
	fmt.Println("Data Preview         :", cfg.ShowPreview)
	fmt.Println("----------------------------------------------")

	if int(cfg.Slice) == len(testdata) {
		fmt.Printf("Expected Slice       : "+Green+"%d"+Reset+"\n", cfg.Slice)
		fmt.Printf("Fetched Size         : "+Green+"%d"+Reset+"\n", len(testdata))
	} else {
		fmt.Printf("Expected Slice       : "+Red+"%d"+Reset+"\n", cfg.Slice)
		fmt.Printf("Fetched Size         : "+Red+"%d"+Reset+"\n", len(testdata))
	}

	fmt.Println("----------------------------------------------")
	fmt.Printf("Requested rate       : "+Green+"%.3f MiB/s"+Reset+" (payload target)\n", expectedbr)
	fmt.Printf("Analysis prefix cap  : %d bytes (0 = full frame)\n", cfg.AnalysisBytes)
	fmt.Printf("Analysis interval    : %s (0 = every frame)\n", cfg.AnalysisInterval)
	fmt.Printf("Display interval     : %s\n", cfg.DisplayInterval)
	fmt.Printf("Pre-flight analysed  : %d / %d bytes\n", result.N, len(testdata))

	// fmt.Printf("Bitrate              : %f\n", result.Rate)
	// fmt.Printf("Shannon entropy      : %.6f bits/byte\n", result.Shannon)
	// fmt.Printf("Chi²                 : %.3f (p=%.6f)\n", result.Chi2, result.Chi2P)
	// fmt.Printf("Monobit p-value      : %.6f\n", result.MonobitP)
	// fmt.Printf("Serial ratio         : %.6f (p=%.6f)\n", result.SerialR, result.SerialP)
	// fmt.Println("TLS Certificate      :", cfg.CertFile)
	// fmt.Println("TLS Key              :", cfg.KeyFile)
	// fmt.Println("----------------------------------------------")

	if result.N > 0 {
		fmt.Printf("PASS                 : " + Green + "Link Established" + Reset + "\n")
	} else {
		fmt.Printf("PASS                 : " + Red + "Link down / No data fetched" + Reset + "\n")
	}

	fmt.Println("----------------------------------------------")

	if !waitContext(ctx, 3*time.Second) {
		return nil
	}

	fmt.Print("\033[H\033[2J")
	fmt.Println("\n----------------------------------------------")

	diagnosticCtx, cancelDiagnostic := context.WithTimeout(ctx, 3*time.Second)

	if cfg.Mode == modePull {
		err = client.TestEntropyHTTP(diagnosticCtx, refhttpurl)
	} else {
		err = client.TestEntropyWS(diagnosticCtx, refwsurl)
	}

	cancelDiagnostic()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// A completed diagnostic time budget is expected for an ongoing stream.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("entropy diagnostic: %w", err)
	}

	fmt.Println("----------------------------------------------")
	fmt.Print("\a")

	if !waitContext(ctx, 2*time.Second) {
		return nil
	}

	return runMonitor(ctx, cfg, refhttpurl, refwsurl)
}
