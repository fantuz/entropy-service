package diag

import (
	"fmt"
	"io"
	"os"
)

// perfect RNG -> 1 / 64 ≈ 0.015625

type TransitionMatrix struct {
	Matrix [64][64]int
	Total  int
}

func BuildTransitionMatrix(data []byte) TransitionMatrix {
	var tm TransitionMatrix

	if len(data) < 2 {
		return tm
	}

	for i := range len(data) - 1 {
		a := int(data[i]) / 4
		b := int(data[i+1]) / 4

		tm.Matrix[a][b]++
		tm.Total++
	}

	return tm
}

func (tm *TransitionMatrix) Probability(i, j int) float64 {
	if tm.Total == 0 {
		return 0
	}

	return float64(tm.Matrix[i][j]) / float64(tm.Total)
}

func (tm *TransitionMatrix) PrintHeatmap() {
	tm.PrintHeatmapTo(os.Stdout)
}

func (tm *TransitionMatrix) PrintHeatmapTo(out io.Writer) {
	peak := 0

	for i := range 64 {
		for j := range 64 {
			if tm.Matrix[i][j] > peak {
				peak = tm.Matrix[i][j]
			}
		}
	}

	if peak == 0 {
		peak = 1
	}

	fmt.Fprintln(out, "\nTransition Matrix (64x64)")
	fmt.Fprintln(out, "--------------------------")

	for i := range 32 {
		fmt.Fprint(out, "                 ")

		for j := range 64 {
			v := float64(tm.Matrix[i][j]) / float64(peak)

			switch {
			case v > 0.75:
				fmt.Fprint(out, "█")

			case v > 0.5:
				fmt.Fprint(out, "▓")

			case v > 0.25:
				fmt.Fprint(out, "▒")

			case v > 0.1:
				fmt.Fprint(out, "░")

			default:
				fmt.Fprint(out, " ")
			}
		}

		fmt.Fprintln(out)
	}
}
