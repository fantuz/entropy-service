package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	//"os/signal"
	//"syscall"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/fantuz/entropy-service/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/entropy-client/internal/device"
	"github.com/fantuz/entropy-service/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/entropy-client/internal/tests"
	//"github.com/fantuz/entropy-service/entropy-client/internal/pipe"
	"github.com/gorilla/websocket"
)

type Stats struct {
	TotalBytes int
	Rate       float64
	Entropy    float64
	Hist       [256]int
}

const Reset = "\033[0m"
const Red = "\033[31m"
const Green = "\033[32m"
const Yellow = "\033[33m"
const Blue = "\033[34m"
const Magenta = "\033[35m"
const Cyan = "\033[36m"
const Gray = "\033[37m"
const White = "\033[97m"

func computeEntropy(data []byte, hist *[256]int) float64 {

	for i := range hist {
		hist[i] = 0
	}

	for _, b := range data {
		hist[b]++
	}

	total := float64(len(data))
	var h float64

	for _, c := range hist {

		if c == 0 {
			continue
		}

		p := float64(c) / total
		h -= p * math.Log2(p)
	}

	return h
}

func drawHistogram(hist *[256]int) {

	max := 0
	for i := 0; i < 16; i++ {
		if hist[i] > max {
			max = hist[i]
		}
	}

	for i := 0; i < 16; i++ {

		barLen := 0
		if max > 0 {
			barLen = hist[i] * 8 / max
		}

		bar := ""
		for j := 0; j < barLen; j++ {
			bar += "█"
		}

		fmt.Printf("%02X %-10s\n", i, bar)
	}
}

func drawUI(stats *Stats, start time.Time, data []byte) {

	fmt.Print("\033[H\033[2J")
	fmt.Println(Gray + "EntropyCTL Monitor" + Reset)
	fmt.Println()

	//t := (*diag.RateMeter).RateMbps()
	//t := diag.NewRateMeter()
	//t.Update(int(stats.Rate))

	//fmt.Printf("Dashboard : %v\n", t)
	//fmt.Printf("Dashboard : %q\n", t)
	//fmt.Printf("Rate               : %.6f MB/s\n", stats.Rate/1024)
	//fmt.Printf("Fetched            : %d KB\n", (stats.TotalBytes/1024))
	//fmt.Printf("Meter     : %d\n", &t.Meter)
	//fmt.Printf("Rate      : %d\n", &t.Rate)
	//fmt.Printf("Fetched R : %d\n", &t.Bytes)
	//fmt.Printf("Entropy            : %.4f bits/byte\n", stats.Entropy)

	status := Green + "OK" + Reset
	if stats.Entropy < 7.999 {
		status = Red + "WARN" + Reset
	}

	fmt.Printf("Status             : %s\n", status)
	fmt.Println()

	//(*diag.RateMeter).Update(diag.RateMeter.rate, 1024)
	//t.Update(len(data))
	tests.RunAll(data)
}

func main() {

	/*
		rctx, rstop := signal.NotifyContext(
			context.Background(),
			os.Interrupt,
			syscall.SIGTERM,
		)
		defer rstop()
	*/

	cfg := config.ParseConfig()
	//flag.Parse()

	if cfg.WriteOut {
		//panic("Write Out Device issue")
	}

	if cfg.ShowDebug {
		//panic("Debug forbidden ;)")
	}

	if cfg.Slice < 1 || cfg.Slice > 33554432 {
		panic(Red + "Slice of data fetched through websocket shall be between 1B and 32 MB" + Reset)
	}

	fps := strconv.FormatInt(cfg.Refresh.Milliseconds(), 10)
	rbytes := strconv.FormatInt(cfg.Slice, 10)

	var refhttpurl = cfg.HTTPUrl + "?bytes=" + rbytes + "&refresh=" + fps
	var refwsurl = cfg.WSUrl + "?bytes=" + rbytes + "&refresh=" + fps
	var fpsdigit = 1000 / int(cfg.Refresh.Milliseconds())
	var expectedbr float64 = (float64(cfg.Slice) * float64(fpsdigit)) / 1024 / 1024
	var opt int = 0

	timeoutctx, timeoutcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer timeoutcancel()

	fmt.Print("\033[H\033[2J")
	fmt.Print("\a")

	testdata, _ := client.FetchEntropySimple(&refhttpurl, &cfg.Slice)

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
	//test := diag.NewRateMeter

	timeoutcancel()

	fmt.Println(Gray + "EntropyCTL Monitor" + Reset)
	fmt.Println()
	fmt.Println("*--------------------------------------------*")
	fmt.Println("|  Pre-flight diagnostics on entropy source  |")
	fmt.Println("*--------------------------------------------*")
	fmt.Println("Framerate            : " + strconv.Itoa(fpsdigit) + " FPS (1 frame every " + strconv.Itoa(int(cfg.Refresh.Milliseconds())) + "ms)")
	fmt.Println("Refresh (seconds)    :", cfg.Refresh)
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

	//fmt.Printf("Bitrate              : %f\n", result.Rate)
	//fmt.Printf("Shannon entropy      : %.6f bits/byte\n", result.Shannon)
	//fmt.Printf("Chi²                 : %.3f (p=%.6f)\n", result.Chi2, result.Chi2P)
	//fmt.Printf("Monobit p-value      : %.6f\n", result.MonobitP)
	//fmt.Printf("Serial ratio         : %.6f (p=%.6f)\n", result.SerialR, result.SerialP)
	//fmt.Println("TLS Certificate      :", cfg.CertFile)
	//fmt.Println("TLS Key              :", cfg.KeyFile)
	//fmt.Println("----------------------------------------------")

	if result.N > 0 {
		fmt.Printf("PASS                 : " + Green + "Link Established" + Reset + "\n")
	} else {
		fmt.Printf("PASS                 : " + Red + "Link down / No data fetched" + Reset + "\n")
	}

	fmt.Println("----------------------------------------------")

	time.Sleep(3 * time.Second)
	fmt.Print("\033[H\033[2J")

	wsconn, _, wscerr := websocket.DefaultDialer.Dial(refwsurl, nil)
	if wscerr != nil {
		fmt.Fprintf(os.Stderr, "fetch error: %v\n", wscerr)
		fmt.Printf("error: %v\n", refhttpurl)
		fmt.Printf("error: %v\n", refwsurl)
		panic(wscerr)
		//os.Exit(1)
	}

	defer wsconn.Close()

	fmt.Println("\n----------------------------------------------")

	ctxA, cancelA := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelA()

	errA := client.TestEntropyHTTP(ctxA, refhttpurl)
	if errA != nil {
		fmt.Fprintf(os.Stderr, "fetch error: %v\n", errA)
		log.Fatal(errA)
		time.Sleep(3 * time.Second)
	}

	fmt.Println("----------------------------------------------")

	ctxB, cancelB := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelB()

	errB := client.TestEntropyWS(ctxB, refwsurl)
	if errB != nil {
		fmt.Fprintf(os.Stderr, "fetch error: %v\n", errB)
		log.Fatal(errB)
		time.Sleep(3 * time.Second)
	}

	fmt.Println("----------------------------------------------")

	cancelA()
	cancelB()

	fmt.Print("\a")
	time.Sleep(2 * time.Second)

	/*
		resultA := diag.RunDiagnostics(testdata)
		if resultA.Pass == true {
			fmt.Printf("PASS                 : " + Green + "%v" + Reset + "\n", resultA.Pass)
		} else {
			fmt.Printf("PASS                 : " + Red + "%v"+ Reset + "\n", resultA.Pass)
		}
	*/

	select {
	case <-timeoutctx.Done():
		{
			cancelA()
			cancelB()
			timeoutcancel()
			wsconn.Close()
			//fmt.Println("HERE-CASE-CTX-CONTINUE:")
			//return
		}
	default:
		{
			//fmt.Println("there-CASE-CTX-CONTINUE:")
			//continue

			/*
				dataone, msg, wserr := wsconn.ReadMessage()
				if wserr != nil {
					fmt.Fprintf(os.Stderr, "fetch error: %v\n", wserr)
					os.Exit(1)
					//panic(err)
				} else {
					fmt.Printf("MSG : %v\n", len(msg))
					fmt.Printf("DATA: %v\n", dataone)
				}
				fmt.Println("HERE-CASE-CTX-CONTINUE:")
				wsconn.Close()
			*/
		}
	}

	stats := Stats{}

	startprg := time.Now()

	graph := diag.NewEntropyGraph(60)
	dashboard := diag.NewDashboard(60)

	tickerh := time.NewTicker(time.Duration(cfg.Refresh))
	//httpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
	//httpCtx, httpCancel := context.WithCancel(context.Background())
	//defer httpCancel()
	//httpCancel()

	tickerw := time.NewTicker(time.Duration(cfg.Refresh))
	wsctx, wscancel := context.WithCancel(context.Background())
	defer wscancel()
	// wscancel()

	stream, sterr := client.StreamEntropy(wsctx, cfg.WSUrl)
	if sterr != nil {
		panic(sterr)
	}

	defer tickerw.Stop()
	//atomic.AddUint64(&diag.wsCRequests, +1)

	fmt.Print("\a")

	startout := time.Now()

	for {
		start := time.Now()

		//defer wsconn.Close()
		//defer wsconn.Close()
		//elapsed := time.Since(start).Seconds()
		//stats.Rate = float64(stats.TotalBytes) / elapsed
		if cfg.Mode == "stream" {
			startws := time.Now()

			select {
			case <-wsctx.Done():
				//return
				panic("critical point reached")
				//continue
			case <-tickerw.C:
				for stdata := range stream {

					atomic.AddUint64(&diag.BytesFetched, uint64(len(stdata)))
					stats.TotalBytes += len(stdata)
					stats.Entropy = computeEntropy(stdata, &stats.Hist)

					drawUI(&stats, startws, stdata)

					if cfg.WriteOut && len(stdata) > 0 {
						deverr := device.Write("/dev/urandom", stdata)
						if deverr != nil {
							fmt.Println("device write error:", deverr)
						}
					}

					if cfg.ShowMatrix && len(stdata) > 0 {
						tm := diag.BuildTransitionMatrix(stdata)
						tm.PrintHeatmap()
						opt++
					}

					if cfg.ShowPreview && len(stdata) > 0 {
						n := 64
						if len(stdata) < n {
							n = len(stdata)
						}
						fmt.Println("preview (hex):", hex.EncodeToString(stdata[:n]))
						fmt.Println(hex.EncodeToString(stdata[:32]))
						//fmt.Println(hex.Dump(stdata[:32]))
						opt++
					}

					if cfg.ShowGraph && len(stdata) > 0 {
						r := diag.RunDiagnostics(stdata)
						graph.Add(r.Shannon)
						graph.Render()
						opt++
					}

					if cfg.ShowDashboard && len(stdata) > 0 {
						r := diag.RunDiagnostics(stdata)
						dashboard.Add(r)
						dashboard.Render()
						opt++
					}

					if cfg.ShowHistogram && len(stdata) > 0 {
						fmt.Println("\nHistogram (top 16)")
						drawHistogram(&stats.Hist)
						//bgraph := diag.PrintHistogram64(, stdata)
						//bgraph.Render()
						diag.PrintHistogram64(32, stdata) // TODO: fix bucketsize
						opt++
					}

					hrtot := time.Since(startprg).Milliseconds()
					hrreal := time.Since(startout).Milliseconds()
					elapsedws := time.Since(startws).Milliseconds()
					stats.Rate = float64(stats.TotalBytes) / float64(elapsedws)
					//fmt.Println("Program rate         :", r.Rate, "MB/s")
					//fmt.Println("Program notes        :", r.Notes, "ms")
					//fmt.Printf("Entropy            : %q", &stats.Entropy, "ms")

					if cfg.ShowDebug && len(stdata) > 0 {
						fmt.Printf("received           : %d bytes\n", len(stdata))
						//btot := atomic.LoadUint64(&diag.BytesFetched)
						//fmt.Println("real RECV          :", (btot/3)+uint64(len(stdata)))
						//fmt.Println("WS cycle runtime   :", hrtot-elapsedws, "ms")
						fmt.Println("WS cycle runtime   :", hrtot-elapsedws-int64(cfg.Refresh)/1000000, "ms")
						fmt.Println("Total runtime      :", hrreal, "ms")
					}
					if opt > 0 {
						time.Sleep(cfg.Refresh / 2) //* time.Millisecond)
					}
				}
			/*
				case <-rctx.Done():
					log.Println("shutdown signal received")
					log.Println("shutdown complete")
			*/
			default:
				{
					continue
				}
			}
		}

		if cfg.Mode == "pull" {
			select {
			//case <-httpCtx.Done():
			//	//httpCancel()
			//	//return
			//	continue
			//case <-wsctx.Done():
			//	return
			//	//continue
			case <-tickerh.C:

				//data, err := client.FetchEntropy(httpCtx, refhttpurl, cfg.Slice)
				data, err := client.FetchEntropySimple(&refhttpurl, &cfg.Slice)

				if err != nil {
					fmt.Fprintf(os.Stderr, "fetch error: %v\n", err)
					os.Exit(1)
					//panic(err)
				} else {
					atomic.AddUint64(&diag.BytesFetched, uint64(len(data)))
					//atomic.AddUint64(&diag.httpCRequests, +1)
				}

				//defer tickerh.Stop()

				stats.TotalBytes += len(data)

				elapsed := time.Since(start).Seconds()
				stats.Rate = float64(stats.TotalBytes) / elapsed

				stats.Entropy = computeEntropy(data, &stats.Hist)
				drawUI(&stats, start, data)

				if cfg.WriteOut && len(data) > 0 {
					deverr := device.Write("/dev/urandom", data)
					if deverr != nil {
						fmt.Println("device write error:", deverr)
					}
				}

				if cfg.ShowMatrix && len(data) > 0 {
					tm := diag.BuildTransitionMatrix(data)
					tm.PrintHeatmap()
					opt++
				}

				if cfg.ShowPreview && len(data) > 0 {
					n := 64
					if len(data) < n {
						n = len(data)
					}
					fmt.Println("preview (hex):", hex.EncodeToString(data[:n]))
					fmt.Println(hex.EncodeToString(data[:32]))
					//fmt.Println(hex.Dump(data[:32]))
					opt++
				}

				if cfg.ShowGraph && len(data) > 0 {
					r := diag.RunDiagnostics(data)
					graph.Add(r.Shannon)
					graph.Render()
					opt++
				}

				if cfg.ShowDashboard && len(data) > 0 {
					r := diag.RunDiagnostics(data)
					dashboard.Add(r)
					dashboard.Render()
					opt++
				}

				if cfg.ShowHistogram && len(data) > 0 {
					fmt.Println("\nHistogram (top 16)")
					drawHistogram(&stats.Hist)
					//bgraph := diag.PrintHistogram64(, data)
					//bgraph.Render()
					diag.PrintHistogram64(32, data) // TODO: fix bucketsize
					opt++
				}

				hrinside := time.Since(start).Milliseconds()
				hrreal := time.Since(startout).Milliseconds()
				//hrtot := time.Since(startprg).Milliseconds()
				//fmt.Println("Program notes        :", r.Notes, "ms")
				//fmt.Println("Program rate         :", r.Rate, "MB/s")
				if cfg.ShowDebug && len(data) > 0 {
					fmt.Printf("received           : %d bytes\n", len(data))
					//btot := atomic.LoadUint64(&diag.BytesFetched)
					//fmt.Println("real RECV          :", btot/3)
					fmt.Println("HTTP cycle runtime :", hrinside, "ms")
					fmt.Println("Total runtime      :", hrreal, "ms")
					opt++
				}

				if opt > 0 {
					time.Sleep(cfg.Refresh / 2) //* time.Millisecond)
				}

			/*
				case <-rctx.Done():
					log.Println("shutdown signal received")
					log.Println("shutdown complete")
			*/

			default:
				{
					wscancel()
					continue
				}
			}
		}
	}
}
