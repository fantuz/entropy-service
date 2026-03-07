package main

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"time"
	"encoding/hex"
)

const (
	serverURL = "http://127.0.0.1:8080/v1/data/random?bytes=1024"
	refresh   = time.Second/4
)

type Stats struct {
	TotalBytes int
	Rate       float64
	Entropy    float64
	Hist       [256]int
}

func fetchEntropy() ([]byte, error) {

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
	fmt.Println("press Ctrl+C to exit")

	fmt.Println(hex.EncodeToString(data))
	fmt.Println(hex.Dump(data))
	//pass := data.EncodeToString(data[:])
	//fmt.Println(pass)
	//fmt.Println(data)
	//fmt.Println(string(data))
}

func main() {

	stats := Stats{}

	start := time.Now()

	for {

		data, err := fetchEntropy()

		if err != nil {
			fmt.Println("connection error:", err)
			os.Exit(1)
		}

		stats.TotalBytes += len(data)
		//fmt.Println(data)

		stats.Entropy = computeEntropy(data, &stats.Hist)

		elapsed := time.Since(start).Seconds()
		stats.Rate = float64(stats.TotalBytes) / elapsed

		drawUI(&stats, start, data)

		time.Sleep(refresh)
	}
}
