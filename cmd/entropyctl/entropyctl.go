package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	"os"
	"sync/atomic"
	"time"

	"entropy-service/internal/client"
	"entropy-service/internal/device"
	"entropy-service/internal/diag"
	"entropy-service/internal/tests"
	"github.com/gorilla/websocket"
	//"entropy-service/internal/pipe"
)

/*
	"io"
	"math"
	"net/http"
	"time"
	"sync/atomic"
	"encoding/hex"

entropy-client \
   -device /dev/urandom \
   -stdout \
   -tests
*/

/*
var size = flag.Int("bytes", 1048576, "bytes to fetch")

const (
	serverURL = "http://127.0.0.1:8080/v1/data/random?bytes=1048576"
	refresh   = time.Second / 2
)
*/

// var wsurl = "http://127.0.0.1:8080/stream.html?bytes=1048576"
const (
	//wsurl = "http://127.0.0.1:8080/stream.html?bytes=1048576"
	wsurl = "ws://localhost:8080/stream?bytes=1048576"
)

type Stats struct {
	TotalBytes int
	Rate       float64
	Entropy    float64
	Hist       [256]int
}

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
	fmt.Println("EntropyCTL Monitor")
	//fmt.Println("Server :", serverURL)
	fmt.Println()

	//fmt.Printf("Rate    : %.1f MB/s\n", stats.Rate/1024/1024)
	fmt.Printf("Bitrate : %.2f Mbit/s\n", stats.Rate/1024/1024)
	fmt.Printf("Fetched : %d KB\n", stats.TotalBytes/1024)
	fmt.Printf("Entropy : %.4f bits/byte\n", stats.Entropy)

	status := "OK"
	if stats.Entropy < 7.9 {
		status = "WARN"
	}

	fmt.Printf("Status  : %s\n", status)
	fmt.Println()

	tests.RunAll(data)
	//(*diag.RateMeter).Update(diag.RateMeter, 1024)
}

func main() {

	url := flag.String("url", "http://127.0.0.1:8080/v1/data/random?bytes=1048576", "entropy endpoint")
	slice := flag.Int("slice", 1048576, "amount of entropy pre fetch")
	showDebug := flag.Bool("debug", false, "print debug information")
	showPreview := flag.Bool("preview", false, "print hex preview of first 64 bytes")
	showMatrix := flag.Bool("matrix", false, "print 64x64 matrix ")
	showGraph := flag.Bool("graph", false, "print distribution graph")
	showDashboard := flag.Bool("dashboard", false, "print dashboard summaries")
	showHistogram := flag.Bool("histogram", false, "print extended histogram")
	refresh := flag.Duration("refresh", 50*time.Millisecond, "refresh interval for both HTTP and WS")
	mode := flag.String("mode", "pull", "pull|stream")

	flag.Parse()

	// create a cancellable context (useful for timeouts / graceful shutdown)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Print("\033[H\033[2J") // clear screen

	testdata, _ := client.FetchEntropySimple(url, slice)
	result := diag.RunDiagnostics(testdata)

	fmt.Println("Diagnostic summary")
	//fmt.Printf("Bitrate: %f\n", result.Rate)
	fmt.Printf("Bytes: %d\n", result.N)
	fmt.Printf("Shannon entropy: %.6f bits/byte\n", result.Shannon)
	fmt.Printf("Chi²: %.3f (p=%.6f)\n", result.Chi2, result.Chi2P)
	fmt.Printf("Monobit p-value: %.6f\n", result.MonobitP)
	fmt.Printf("Serial r: %.6f (p=%.6f)\n", result.SerialR, result.SerialP)
	fmt.Printf("PASS: %v\n", result.Pass)
	fmt.Println()

	time.Sleep(3 * time.Second)

	wsconn, _, wscerr := websocket.DefaultDialer.Dial(wsurl, nil)
	if wscerr != nil {
		fmt.Fprintf(os.Stderr, "fetch error: %v\n", wscerr)
		fmt.Printf("error: %v\n", *url)
		fmt.Printf("error: %v\n", wsurl)
		panic(wscerr)
		//os.Exit(1)
	}

	defer wsconn.Close()

	// TODO: add exit here if tests FAIL

	graph := diag.NewEntropyGraph(120) // was 80
	dashboard := diag.NewDashboard(120)

	select {
	case <-ctx.Done():
		{
			//fmt.Println("HERE-CASE-CTX-DONE:")
			cancel()
			//return
		}
	default:
		{
			//fmt.Println("HERE-CASE-CTX-CONTINUE:")
		}
	}

	for {
		iterCtx, iterCancel := context.WithTimeout(context.Background(), 10*time.Second)

		data, err := client.FetchEntropy(iterCtx, *url, *slice)
		//data, err := client.FetchEntropySimple(url, slice)

		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch error: %v\n", err)
			os.Exit(1)
			//panic(err)
		} else {
			atomic.AddUint64(&diag.BytesFetched, uint64(len(data)))
			//addplusbytes := diag.incBytes(len(data))
			//atomic.AddUint64(&diag.httpCRequests, +1)
			//htot := atomic.LoadUint64(&diag.httpCRequests)
			//htot := diag.incHTTP(1)
			//fmt.Println("real HTTP:", htot)
		}

		//defer iterCancel()
		iterCancel()

		/*
			_, msg, wserr := wsconn.ReadMessage()
			if wserr != nil {
				fmt.Fprintf(os.Stderr, "fetch error: %v\n", wserr)
				os.Exit(1)
				//panic(err)
			} else {
				fmt.Printf("MSG: %v\n", len(msg))
			}
		*/

		/*
			for data := range msg {
				r := diag.RunDiagnostics(data)
				dashboard.Add(r)
				dashboard.Render()
				graph.Add(r.Shannon)
				graph.Render()
			}
		*/

		if *mode == "stream" {
			stream, sterr := client.StreamEntropy(wsurl)
			if sterr != nil {
				panic(sterr)
			}
			for stdata := range stream {
				//diag.RunDiagnostics(data)
				r := diag.RunDiagnostics(stdata)
				dashboard.Add(r)
				dashboard.Render()
				graph.Add(r.Shannon)
				graph.Render()
				fmt.Printf("MSG: %v\n", len(stdata))
			}
			/*
				for stdata := range stream {
					//r := diag.RunDiagnostics(data)
					//dashboard.Add(r)
					//dashboard.Render()
				}
			*/
		}

		// ... call your diag / tests / UI code here
		//_ = data

		// optional device write
		deverr := device.Write("/dev/urandom", data)
		if deverr != nil {
			fmt.Println("device write error:", deverr)
		}

		stats := Stats{}
		start := time.Now()

		stats.TotalBytes += len(data)

		stats.Entropy = computeEntropy(data, &stats.Hist)

		elapsed := time.Since(start).Seconds()
		stats.Rate = float64(stats.TotalBytes) / elapsed

		drawUI(&stats, start, data)

		if *showMatrix && len(data) > 0 {
			tm := diag.BuildTransitionMatrix(data)
			tm.PrintHeatmap()
			time.Sleep(50 * time.Millisecond)
		}

		if *showPreview && len(data) > 0 {
			n := 64
			if len(data) < n {
				n = len(data)
			}
			fmt.Println("preview (hex):", hex.EncodeToString(data[:n]))
			fmt.Println(hex.EncodeToString(data[:32]))
			//fmt.Println(hex.Dump(data[:32]))
			// pause slightly so user sees preview in TTY
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
			time.Sleep(500 * time.Millisecond)
		}

		if *showDashboard && len(data) > 0 {
			q := diag.RunDiagnostics(data)
			dashboard.Add(q)
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

		//time.Sleep(50 * time.Millisecond)
		//time.Sleep(refresh)
		//time.Sleep(time.Duration(*refresh * (time.Millisecond)))

		hr := time.Since(start).Milliseconds()
		fmt.Println("runtime:", hr, "ms")

		time.Sleep(time.Duration(*refresh))
	}
}
