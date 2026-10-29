package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/device"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/tests"
)

type Stats struct {
	TotalBytes int
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

func drawHistogram(hist *[256]int) {
	peak := 0
	for i := range 16 {
		if hist[i] > peak {
			peak = hist[i]
		}
	}

	for i := range 16 {
		barLen := 0
		if peak > 0 {
			barLen = hist[i] * 8 / peak
		}

		var bar strings.Builder

		for range barLen {
			bar.WriteString("█")
		}

		fmt.Printf("%02X %-10s\n", i, bar.String())
	}
}

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
		panic(Red + "Slice of data fetched through websocket shall be between 1B and 32 MB" + Reset)
	}

	if cfg.Refresh < time.Millisecond {
		panic(Red + "refresh must be at least 1ms" + Reset)
	}

	fps := strconv.FormatInt(cfg.Refresh.Milliseconds(), 10)
	rbytes := strconv.FormatInt(cfg.Slice, 10)

	refhttpurl := cfg.HTTPUrl + "?bytes=" + rbytes + "&refresh=" + fps
	refwsurl := cfg.WSUrl + "?bytes=" + rbytes + "&refresh=" + fps
	fpsdigit := 1000 / float64(cfg.Refresh.Milliseconds())

	var (
		expectedbr = (float64(cfg.Slice) * float64(fpsdigit)) / 1024 / 1024
		opt        = 0
	)

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

	result := diag.RunDiagnostics(testdata)
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

	if int(cfg.Slice) == result.N {
		fmt.Printf("Expected Slice       : "+Green+"%d"+Reset+"\n", cfg.Slice)
		fmt.Printf("Fetched Size         : "+Green+"%d"+Reset+"\n", result.N)
	} else {
		fmt.Printf("Expected Slice       : "+Red+"%d"+Reset+"\n", cfg.Slice)
		fmt.Printf("Fetched Size         : "+Red+"%d"+Reset+"\n", result.N)
	}

	fmt.Println("----------------------------------------------")
	fmt.Printf("Expected Bitrate     : "+Green+"%.3f MB/s"+Reset+"\n", expectedbr)

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
		return nil
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

	stats := Stats{}

	graph := diag.NewEntropyGraph(60)
	dashboard := diag.NewDashboard(60)

	tickerh := time.NewTicker(cfg.Refresh)
	defer tickerh.Stop()

	wsctx, wscancel := context.WithCancel(ctx)
	defer wscancel()
	// wscancel()

	// stream, sterr := client.StreamEntropy(wsctx, cfg.WSUrl)
	var stream <-chan client.StreamFrame

	if cfg.Mode == modeStream {
		var sterr error

		stream, sterr = client.StreamEntropy(wsctx, refwsurl)
		if sterr != nil {
			return fmt.Errorf("open entropy stream: %w", sterr)
		}
	}

	defer func() {
		wscancel()

		if stream != nil {
			for range stream {
			}
		}
	}()

	fmt.Print("\a")

	startout := time.Now()

	var wsRuntime time.Duration

	for {
		if ctx.Err() != nil {
			return nil
		}

		start := time.Now()

		// defer wsconn.Close()
		// defer wsconn.Close()
		// elapsed := time.Since(start).Seconds()
		// stats.Rate = float64(stats.TotalBytes) / elapsed
		if cfg.Mode == modeStream {
			startws := time.Now()

			select {
			case <-wsctx.Done():
				// panic("critical point reached")
				// continue
				return nil

			// case <-tickerw.C:
			case frame, ok := <-stream:
				if !ok {
					if ctx.Err() != nil {
						return nil
					}

					return errors.New("WebSocket stream closed")
				}

				stdata := frame.Data
				// for stdata := range stream {
				atomic.AddUint64(&diag.BytesFetched, uint64(len(stdata)))
				stats.TotalBytes += len(stdata)
				diagnostics := diag.RunDiagnostics(stdata)
				stats.Entropy = diagnostics.Shannon
				stats.Hist = diagnostics.Histogram

				drawUI(&stats, &diagnostics)

				if cfg.WriteOut && len(stdata) > 0 {
					deverr := device.Write("/dev/urandom", stdata)
					if deverr != nil {
						fmt.Println("device write error:", deverr)
					}
				}

				if cfg.ShowMatrix && len(stdata) > 0 {
					diagnostics.TMatrix.PrintHeatmap()

					opt++
				}

				if cfg.ShowPreview && len(stdata) > 0 {
					n := min(64, len(stdata))

					fmt.Println("preview (hex):", hex.EncodeToString(stdata[:n]))
					// fmt.Println(hex.EncodeToString(stdata[:32]))
					// fmt.Println(hex.Dump(stdata[:32]))
					opt++
				}

				if cfg.ShowGraph && len(stdata) > 0 {
					graph.Add(diagnostics.Shannon)
					graph.Render()

					opt++
				}

				if cfg.ShowDashboard && len(stdata) > 0 {
					dashboard.Add(diagnostics)
					dashboard.Render()

					opt++
				}

				if cfg.ShowHistogram && len(stdata) > 0 {
					fmt.Println("\nHistogram (top 16)")
					drawHistogram(&stats.Hist)
					// bgraph := diag.PrintHistogram64(, stdata)
					// bgraph.Render()
					diag.PrintHistogram64(32, stdata) // TODO: fix bucketsize

					opt++
				}

				cycleEnd := time.Now()
				totalRuntime := cycleEnd.Sub(startout)
				wsRuntime += cycleEnd.Sub(startws)
				stats.Rate = float64(stats.TotalBytes) / totalRuntime.Seconds()
				// fmt.Println("Program rate         :", r.Rate, "MB/s")
				// fmt.Println("Program notes        :", r.Notes, "ms")
				// fmt.Printf("Entropy            : %q", &stats.Entropy, "ms")

				if cfg.ShowDebug && len(stdata) > 0 {
					fmt.Printf("received           : %d bytes\n", len(stdata))
					// btot := atomic.LoadUint64(&diag.BytesFetched)
					// fmt.Println("real RECV          :", (btot/3)+uint64(len(stdata)))
					// fmt.Println("WS cycle runtime   :", hrtot-elapsedws, "ms")
					fmt.Printf("Last WS receive    : %.3f ms\n", frame.ReceiveWait.Seconds()*1000)
					fmt.Printf("WS runtime         : %.3f ms\n", wsRuntime.Seconds()*1000)
					fmt.Printf("Total runtime      : %.3f ms\n", totalRuntime.Seconds()*1000)
				}

				if opt > 0 && !waitContext(ctx, cfg.Refresh/8) {
					return nil
				}
				// }
				/*
					case <-rctx.Done():
						log.Println("shutdown signal received")
						log.Println("shutdown complete")
				*/
			}
		}

		if cfg.Mode == modePull {
			select {
			case <-ctx.Done():
				return nil
			case <-tickerh.C:
			}
			// Measure the request and full body read, excluding refresh wait and diagnostics.
			httpStarted := time.Now()
			httpCtx, cancelHTTP := context.WithTimeout(ctx, 3*time.Second)
			data, err := client.FetchEntropy(httpCtx, refhttpurl, cfg.Slice)
			httpFetchRuntime := time.Since(httpStarted)

			cancelHTTP()

			if err != nil {
				return fmt.Errorf("fetch entropy: %w", err)
			}

			atomic.AddUint64(&diag.BytesFetched, uint64(len(data)))

			// defer tickerh.Stop()

			stats.TotalBytes += len(data)

			elapsed := time.Since(start).Seconds()
			stats.Rate = float64(stats.TotalBytes) / elapsed

			diagnostics := diag.RunDiagnostics(data)
			stats.Entropy = diagnostics.Shannon
			stats.Hist = diagnostics.Histogram

			drawUI(&stats, &diagnostics)

			if cfg.WriteOut && len(data) > 0 {
				deverr := device.Write("/dev/urandom", data)
				if deverr != nil {
					fmt.Println("device write error:", deverr)
				}
			}

			if cfg.ShowMatrix && len(data) > 0 {
				diagnostics.TMatrix.PrintHeatmap()

				opt++
			}

			if cfg.ShowPreview && len(data) > 0 {
				n := min(64, len(data))

				fmt.Println("preview (hex):", hex.EncodeToString(data[:n]))
				// fmt.Println(hex.EncodeToString(data[:32]))
				// fmt.Println(hex.Dump(data[:32]))
				opt++
			}

			if cfg.ShowGraph && len(data) > 0 {
				graph.Add(diagnostics.Shannon)
				graph.Render()

				opt++
			}

			if cfg.ShowDashboard && len(data) > 0 {
				dashboard.Add(diagnostics)
				dashboard.Render()

				opt++
			}

			if cfg.ShowHistogram && len(data) > 0 {
				fmt.Println("\nHistogram (top 16)")
				drawHistogram(&stats.Hist)
				// bgraph := diag.PrintHistogram64(, data)
				// bgraph.Render()
				diag.PrintHistogram64(32, data) // TODO: fix bucketsize

				opt++
			}

			cycleEnd := time.Now()
			cycleRuntime := cycleEnd.Sub(start)
			totalRuntime := cycleEnd.Sub(startout)
			// hrtot := time.Since(startprg).Milliseconds()
			// fmt.Println("Program notes        :", r.Notes, "ms")
			// fmt.Println("Program rate         :", r.Rate, "MB/s")
			if cfg.ShowDebug && len(data) > 0 {
				fmt.Printf("received           : %d bytes\n", len(data))
				// btot := atomic.LoadUint64(&diag.BytesFetched)
				// fmt.Println("real RECV          :", btot/3)
				fmt.Printf("Last HTTP fetch    : %.3f ms\n", httpFetchRuntime.Seconds()*1000)
				fmt.Printf("HTTP cycle runtime : %.3f ms\n", cycleRuntime.Seconds()*1000)
				fmt.Printf("Total runtime      : %.3f ms\n", totalRuntime.Seconds()*1000)

				opt++
			}

			if opt > 0 && !waitContext(ctx, cfg.Refresh/8) {
				return nil
			}
		}
	}
}
