package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"io"
	"math"
	"net/http"
	"sync/atomic"

	//"entropy-service/internal/client"
	"entropy-service/internal/device"
	"entropy-service/internal/diag"
	"entropy-service/internal/tests"
)

/*
	"io"
	"math"
	"net/http"
	"time"
	"encoding/hex"

entropy-client \
   -url http://127.0.0.1:8080/entropy \
   -bytes 4096 \
   -device /dev/urandom \
   -stdout \
   -tests
*/

var url = flag.String("url", "http://127.0.0.1:8080/v1/data/random", "entropy endpoint")
var size = flag.Int("bytes", 4096, "bytes to fetch")

const (
	serverURL = "http://127.0.0.1:8080/v1/data/random?bytes=65536"
	refresh   = time.Second / 2
)

type Stats struct {
	TotalBytes int
	Rate       float64
	Entropy    float64
	Hist       [256]int
}

func fetchEntropy(endpoint string, quantity *int) ([]byte, error) {

	resp, err := http.Get(serverURL)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)

	return data, err
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
	fmt.Println("Server :", serverURL)
	fmt.Println()

	fmt.Printf("Rate        : %.1f KB/s\n", stats.Rate/1024)
	fmt.Printf("Fetched     : %d KB\n", stats.TotalBytes/1024)
	fmt.Printf("Entropy     : %.4f bits/byte\n", stats.Entropy)

	status := "OK"
	if stats.Entropy < 7.9 {
		status = "WARN"
	}

	fmt.Printf("Status      : %s\n", status)

	fmt.Println()
	fmt.Println("Histogram (top 16)")
	fmt.Println()
	drawHistogram(&stats.Hist)

	fmt.Println()
	//fmt.Println("press Ctrl+C to exit")

	tests.RunAll(data)
	diag.RunDiagnostics(data)
}

func main() {

	endpoint := "http://127.0.0.1:8080/v1/data/random?bytes=16384"
	//url := flag.String("url", "http://127.0.0.1:8080/entropy?bytes=4096", "entropy endpoint")
	slice := flag.Int("slice", 16384, "amount of entropy pre fetch")
	showPreview := flag.Bool("preview", false, "print hex preview of first 64 bytes")
	showMatrix := flag.Bool("matrix", false, "print 64x64 matrix ")
	showGraph := flag.Bool("graph", false, "print Shannon graph")

	flag.Parse()
	fmt.Print("\033[H\033[2J") // clear screen

	testdata, _ := fetchEntropy(endpoint, slice)
	result := diag.RunDiagnostics(testdata)

	fmt.Println("Diagnostic summary")
	fmt.Printf("Bytes: %d\n", result.N)
	fmt.Printf("Shannon entropy: %.6f bits/byte\n", result.Shannon)
	fmt.Printf("Chi²: %.3f (p=%.6f)\n", result.Chi2, result.Chi2P)
	fmt.Printf("Monobit p-value: %.6f\n", result.MonobitP)
	fmt.Printf("Serial r: %.6f (p=%.6f)\n", result.SerialR, result.SerialP)
	fmt.Printf("PASS: %v\n", result.Pass)
	fmt.Println("")

	time.Sleep(3 * time.Second)

	// TODO: add exit here if tests FAIL

	graph := diag.NewEntropyGraph(120)
	//addplushttp := diag.incHTTP()
	//addplusbytes := diag.incBytes(len(data))

	for {

		data, err := fetchEntropy(endpoint, slice)
		//data, err := client.FetchEntropy(endpoint, slice)
		if err != nil {
			fmt.Println("connection error:", err)
			fmt.Println("fetch error:", err)
			os.Exit(1)
			//panic(err)
		} else {
			atomic.AddUint64(&diag.BytesFetched, uint64(len(data)))
			btot := atomic.LoadUint64(&diag.BytesFetched)
			//atomic.AddUint64(&diag.httpCRequests, +1)
			//htot := atomic.LoadUint64(&diag.httpCRequests)
			//htot := incHTTP(1)
			fmt.Println("real RECV:", btot)
			//fmt.Println("real HTTP:", htot)
		}

		// optional device write
		err = device.Write("/dev/urandom", data)
		if err != nil {
			fmt.Println("device write error:", err)
		}

		stats := Stats{}
		start := time.Now()

		stats.TotalBytes += len(data)
		fmt.Println("received:", len(data), "bytes")

		stats.Entropy = computeEntropy(data, &stats.Hist)

		elapsed := time.Since(start).Seconds()
		stats.Rate = float64(stats.TotalBytes) / elapsed

		hr := time.Since(start).Minutes()
		fmt.Println("runtime:", hr, "seconds")

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
			time.Sleep(200 * time.Millisecond)
		}

		//time.Sleep(500 * time.Millisecond)
		//time.Sleep(refresh)

		if *showGraph && len(data) > 0 {
			r := diag.RunDiagnostics(data)
			graph.Add(r.Shannon)
			graph.Render()
			time.Sleep(500 * time.Millisecond)
		}

		//bgraph := diag.PrintHistogram64(data)
		//bgraph.Render()

		time.Sleep(100 * time.Millisecond)
		drawUI(&stats, start, data)

		//diag.PrintHistogram64(32, data) // TODO: fix bucketsize
	}
}
