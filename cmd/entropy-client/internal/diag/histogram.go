package diag

import (
	"fmt"
	"strings"
)

// PrintHistogramCounts reuses the analysed histogram without retaining or
// scanning the frame again. Each of the 32 buckets covers eight byte values.
func PrintHistogramCounts(hist [256]int) {
	var bins [32]int
	for value, count := range hist {
		bins[value/8] += count
	}

	peak := 1
	for _, count := range bins {
		peak = max(peak, count)
	}

	fmt.Println("\nAnalysed byte distribution (32 bins)")

	for i, count := range bins {
		fmt.Printf("%02x-%02x | %s\n", i*8, i*8+7, strings.Repeat("█", count*50/peak))
	}
}

func Histogram64(buckets int, data []byte) [32]int {
	var bins [32]int

	for _, b := range data {
		idx := int(b) / 8 // TODO: fix here, was 4
		bins[idx]++
	}

	return bins
}

func PrintHistogram64(buckets int, data []byte) {
	bins := Histogram64(32, data)

	peak := 0
	for _, v := range bins {
		if v > peak {
			peak = v
		}
	}

	scale := float64(peak) / 50.0
	if scale == 0 {
		scale = 1
	}

	fmt.Println("\nByte distribution (32 bins)")

	for i := range buckets {
		barLen := int(float64(bins[i]) / scale)

		fmt.Printf("%02x-%02x | ",
			i*4,
			i*4+3,
		)

		for range barLen {
			fmt.Print("█")
		}

		fmt.Println()
	}
}
