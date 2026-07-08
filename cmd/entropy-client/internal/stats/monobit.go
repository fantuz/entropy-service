package stats

import (
	"math"
	"math/bits"
)

// S = (#1 − #0)

/*
func Monobit(data []byte) Result {

	ones := 0
	total := len(data) * 8

	for _, b := range data {
		ones += bits.OnesCount8(b)
	}

	zeros := total - ones
	s := math.Abs(float64(ones-zeros)) / math.Sqrt(float64(total))

	p := math.Erfc(s / math.Sqrt2)

	return Result{
		Name:      "monobit",
		Statistic: s,
		PValue:    p,
		Passed:    p > 0.01,
	}
}
*/

func Monobit(data []byte) Result {
	var ones int

	for _, b := range data {
		ones += bits.OnesCount8(b)
	}

	totalBits := len(data) * 8
	zeros := totalBits - ones

	s := float64(ones - zeros)

	stat := math.Abs(s) / math.Sqrt(float64(totalBits))

	p := math.Erfc(stat / math.Sqrt2)

	return Result{
		Name:      "monobit",
		Statistic: stat,
		PValue:    p,
		Passed:    p > 0.01,
	}
}
