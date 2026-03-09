package diag

import "fmt"

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

	max := 0
	for _, v := range bins {
		if v > max {
			max = v
		}
	}

	scale := float64(max) / 50.0
	if scale == 0 {
		scale = 1
	}

	fmt.Println("\nByte distribution (32 bins)")

	for i := 0; i < buckets; i++ {

		barLen := int(float64(bins[i]) / scale)

		fmt.Printf("%02x-%02x | ",
			i*4,
			i*4+3,
		)

		for j := 0; j < barLen; j++ {
			fmt.Print("█")
		}

		fmt.Println()
	}
}
