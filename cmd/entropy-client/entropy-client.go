package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/fantuz/entropy-service/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/entropy-client/internal/device"
	"github.com/fantuz/entropy-service/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/entropy-client/internal/tests"
	//"github.com/fantuz/entropy-service/entropy-client/internal/pipe"
	"github.com/gorilla/websocket"
)

/*
	"math"
	"time"
	"sync/atomic"
	"encoding/hex"
*/

/*
entropy-client -device /dev/urandom -stdout -tests
*/

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
	//ex diag.Dashboard
	t := diag.NewRateMeter()
	t.Update(int(stats.Rate))

	//fmt.Printf("Dashboard : %v\n", t)
	//fmt.Printf("Dashboard : %q\n", t)
	fmt.Printf("Rate      : %.6f MB/s\n", stats.Rate/1024/1024)
	fmt.Printf("Fetched   : %d KB\n", stats.TotalBytes/1024)
	//fmt.Printf("Meter     : %d\n", &t.Meter)
	//fmt.Printf("Rate      : %d\n", &t.Rate)
	//fmt.Printf("Fetched R : %d\n", &t.Bytes)
	fmt.Printf("Entropy   : %.4f bits/byte\n", stats.Entropy)

	status := Green + "OK" + Reset
	if stats.Entropy < 7.9 {
		status = Red + "WARN" + Reset
	}

	fmt.Printf("Status    : %s\n", status)
	fmt.Println()

	//(*diag.RateMeter).Update(diag.RateMeter.rate, 1024)
	t.Update(len(data))
	tests.RunAll(data)
}

func main() {

	writeOut := flag.Bool("write-out", false, "write out the received entropy back to local /dev/urandom (useful for low-entropy or isolated systems)")
	slice := flag.Int64("slice", 2097152, "amount of entropy pre fetch")
	showDebug := flag.Bool("debug", false, "print debug information")
	showPreview := flag.Bool("preview", false, "print hex preview of first 64 bytes")
	showMatrix := flag.Bool("matrix", false, "print 64x64 matrix ")
	showGraph := flag.Bool("graph", false, "print distribution graph")
	showDashboard := flag.Bool("dashboard", false, "print dashboard summaries")
	showHistogram := flag.Bool("histogram", false, "print extended histogram")
	refresh := flag.Duration("refresh", 50*time.Millisecond, "refresh interval for both HTTP and WS")
	mode := flag.String("mode", "pull", "pull|stream")

	//var size = flag.Int("bytes", 1048576, "bytes to fetch")
	url := flag.String("url", "http://127.0.0.1:8080/v1/data/random?bytes=1048576", "entropy endpoint")
	wsurl := flag.String("wsurl", "ws://127.0.0.1:8080/stream?bytes=1048576", "entropy endpoint")
	fps := strconv.FormatInt(refresh.Milliseconds(), 10)

	var refwsurl = *wsurl + "&refresh=" + fps

	flag.Parse()

	// create a cancellable context (useful for timeouts / graceful shutdown)
	timeoutctx, timeoutcancel := context.WithTimeout(context.Background(), 10*time.Second)

	defer timeoutcancel()
	//timeoutcancel()

	fmt.Print("\033[H\033[2J") // clear screen

	testdata, _ := client.FetchEntropySimple(url, slice)
	result := diag.RunDiagnostics(testdata)
	//test := diag.NewRateMeter

	var fpsdigit int
	fpsdigit = 1000 / int(refresh.Milliseconds())

	fmt.Println(Gray + "EntropyCTL Monitor" + Reset)
	fmt.Println()
	fmt.Println("*--------------------------------------------*")
	fmt.Println("|  Pre-flight diagnostics on entropy source  |")
	fmt.Println("*--------------------------------------------*")
	fmt.Println("Framerate       : " + strconv.Itoa(fpsdigit) + " FPS (1 frame every " + strconv.Itoa(int(refresh.Milliseconds())) + "ms)")
	fmt.Println("HTTP URL        : " + *url)
	fmt.Println("WS URL          : " + *wsurl)
	fmt.Println("WS URL with REF : " + refwsurl)
	fmt.Println("----------------------------------------------")
	//fmt.Println("Program runtime      :", hrtot, "ms")
	fmt.Println("Write-out to /dev    :", *writeOut)
	fmt.Println("Program mode         :", *mode)
	fmt.Println("----------------------------------------------")
	//fmt.Printf("Bitrate         : %d\n", test)
	//fmt.Printf("Bitrate         : %f\n", result.Rate)
	fmt.Printf("Slice           : %d\n", int(*slice))
	fmt.Printf("Bytes           : %d\n", result.N)
	fmt.Printf("Shannon entropy : %.6f bits/byte\n", result.Shannon)
	fmt.Printf("Chi²            : %.3f (p=%.6f)\n", result.Chi2, result.Chi2P)
	fmt.Printf("Monobit p-value : %.6f\n", result.MonobitP)
	fmt.Printf("Serial ratio    : %.6f (p=%.6f)\n", result.SerialR, result.SerialP)
	fmt.Println("----------------------------------------------")
	fmt.Println()

	if result.Pass == true {
		fmt.Printf("PASS            : "+Green+"%v"+Reset+"\n", result.Pass)
	} else {
		fmt.Printf("PASS            : "+Red+"%v"+Reset+"\n", result.Pass)
	}

	time.Sleep(5 * time.Second)

	startprg := time.Now()

	wsconn, _, wscerr := websocket.DefaultDialer.Dial(refwsurl, nil)
	if wscerr != nil {
		fmt.Fprintf(os.Stderr, "fetch error: %v\n", wscerr)
		fmt.Printf("error: %v\n", *url)
		fmt.Printf("error: %v\n", refwsurl)
		panic(wscerr)
		//os.Exit(1)
	}

	defer wsconn.Close()

	// TODO: add exit here if tests FAIL

	// number of columns, also 120 is OK
	graph := diag.NewEntropyGraph(80)
	dashboard := diag.NewDashboard(80)

	select {
	case <-timeoutctx.Done():
		{
			//fmt.Println("HERE-CASE-CTX-DONE:")
			timeoutcancel()
			//return
		}
	default:
		{
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
				//fmt.Println("HERE-CASE-CTX-CONTINUE:")
				wsconn.Close()
			*/
		}
	}

	ticker := time.NewTicker(time.Duration(5) * time.Millisecond)

	for {
		start := time.Now()

		stats := Stats{}

		httpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer httpCancel()
		//httpCancel()

		wsctx, wscancel := context.WithCancel(context.Background())
		defer wscancel()
		// wscancel()

		//defer wsconn.Close()
		//defer wsconn.Close()
		defer ticker.Stop()

		//elapsed := time.Since(start).Seconds()
		//stats.Rate = float64(stats.TotalBytes) / elapsed
		if *mode == "stream" {
			stream, sterr := client.StreamEntropy(wsctx, refwsurl, 32)
			if sterr != nil {
				panic(sterr)
			}
			startws := time.Now()

			select {
			case <-httpCtx.Done():
				return
				continue
			case <-wsctx.Done():
				//return
				continue
			case <-ticker.C:
				for stdata := range stream {
					stats.TotalBytes += len(stdata)
					stats.Entropy = computeEntropy(stdata, &stats.Hist)

					drawUI(&stats, startws, stdata)

					// optional device write
					if *writeOut && len(stdata) > 0 {
						deverr := device.Write("/dev/urandom", stdata)
						if deverr != nil {
							fmt.Println("device write error:", deverr)
						}
					}

					if *showMatrix && len(stdata) > 0 {
						tm := diag.BuildTransitionMatrix(stdata)
						tm.PrintHeatmap()
						time.Sleep(200 * time.Millisecond)
					}

					if *showPreview && len(stdata) > 0 {
						n := 64
						if len(stdata) < n {
							n = len(stdata)
						}
						fmt.Println("preview (hex):", hex.EncodeToString(stdata[:n]))
						fmt.Println(hex.EncodeToString(stdata[:32]))
						//fmt.Println(hex.Dump(stdata[:32]))
						time.Sleep(500 * time.Millisecond)
					}

					if *showDebug && len(stdata) > 0 {
						fmt.Printf("received %d bytes\n", len(stdata))
						btot := atomic.LoadUint64(&diag.BytesFetched)
						fmt.Println("real RECV:", btot)
					}

					if *showGraph && len(stdata) > 0 {
						r := diag.RunDiagnostics(stdata)
						graph.Add(r.Shannon)
						graph.Render()
						time.Sleep(100 * time.Millisecond)
					}

					if *showDashboard && len(stdata) > 0 {
						r := diag.RunDiagnostics(stdata)
						dashboard.Add(r)
						dashboard.Render()
						time.Sleep(100 * time.Millisecond)
					}

					if *showHistogram && len(stdata) > 0 {
						fmt.Println("\nHistogram (top 16)")
						drawHistogram(&stats.Hist)
						//bgraph := diag.PrintHistogram64(, stdata)
						//bgraph.Render()
						diag.PrintHistogram64(32, stdata) // TODO: fix bucketsize
						time.Sleep(200 * time.Millisecond)
					}

					hrtot := time.Since(startprg).Milliseconds()
					elapsedws := time.Since(startws).Milliseconds()
					stats.Rate = float64(stats.TotalBytes) / float64(elapsedws)
					//fmt.Println("Program rate       :", r.Rate, "MB/s")
					//fmt.Println("Program rate         :", r.Rate, "MB/s")
					fmt.Println("WS Routine runtime   :", hrtot-elapsedws, "ms")
					//fmt.Println("Program notes        :", r.Notes, "ms")
					//fmt.Printf("Entropy            : %q", &stats.Entropy, "ms")
					continue
				}
			default:
				{
					continue
				}
			}
		}

		if *mode == "pull" {
			//data, err := client.FetchEntropySimple(url, slice)
			data, err := client.FetchEntropy(httpCtx, *url, *slice)

			if err != nil {
				fmt.Fprintf(os.Stderr, "fetch error: %v\n", err)
				os.Exit(1)
				//panic(err)
			} else {
				atomic.AddUint64(&diag.BytesFetched, uint64(len(data)))
				//atomic.AddUint64(&diag.httpCRequests, +1)
			}

			select {
			case <-httpCtx.Done():
				//return
				//continue
			case <-wsctx.Done():
				//return
				continue
			case <-ticker.C:

				stats.TotalBytes += len(data)
				elapsed := time.Since(start).Seconds()
				stats.Rate = float64(stats.TotalBytes) / elapsed
				stats.Entropy = computeEntropy(data, &stats.Hist)
				drawUI(&stats, start, data)
	
				// optional device write
				if *writeOut && len(data) > 0 {
					deverr := device.Write("/dev/urandom", data)
					if deverr != nil {
						fmt.Println("device write error:", deverr)
					}
				}
	
				if *showMatrix && len(data) > 0 {
					tm := diag.BuildTransitionMatrix(data)
					tm.PrintHeatmap()
					time.Sleep(200 * time.Millisecond)
				}
	
				if *showPreview && len(data) > 0 {
					n := 64
					if len(data) < n {
						n = len(data)
					}
					fmt.Println("preview (hex):", hex.EncodeToString(data[:n]))
					fmt.Println(hex.EncodeToString(data[:32]))
					//fmt.Println(hex.Dump(data[:32]))
					time.Sleep(500 * time.Millisecond)
				}
	
				if *showDebug && len(data) > 0 {
					fmt.Printf("received %d bytes\n", len(data))
					btot := atomic.LoadUint64(&diag.BytesFetched)
					fmt.Println("real RECV:", btot)
				}
	
				if *showGraph && len(data) > 0 {
					r := diag.RunDiagnostics(data)
					graph.Add(r.Shannon)
					graph.Render()
					time.Sleep(100 * time.Millisecond)
				}
	
				if *showDashboard && len(data) > 0 {
					r := diag.RunDiagnostics(data)
					dashboard.Add(r)
					dashboard.Render()
					time.Sleep(100 * time.Millisecond)
				}
	
				if *showHistogram && len(data) > 0 {
					fmt.Println("\nHistogram (top 16)")
					drawHistogram(&stats.Hist)
					//bgraph := diag.PrintHistogram64(, data)
					//bgraph.Render()
					diag.PrintHistogram64(32, data) // TODO: fix bucketsize
					time.Sleep(200 * time.Millisecond)
				}
	
				hrinside := time.Since(start).Milliseconds()
				//hrtot := time.Since(startprg).Milliseconds()
				//fmt.Println("Program notes        :", r.Notes, "ms")
				//fmt.Println("Program rate         :", r.Rate, "ms")
				//fmt.Println("Program runtime      :", hrtot, "ms")
				//fmt.Println("Program rate         :", r.Rate, "MB/s")
				fmt.Println("HTTP Routine runtime :", hrinside, "ms")
				//fmt.Printf("Entropy            : %q", &stats.Entropy, "ms")
				//continue
			default:
				{
				continue
				}
			}
		}
	}
}
