package diag

import "fmt"

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

	fmt.Println("\nTransition Matrix (64x64)")
	fmt.Println("--------------------------")

	for i := range 32 {
		fmt.Print("                 ")

		for j := range 64 {
			v := float64(tm.Matrix[i][j]) / float64(peak)

			switch {
			case v > 0.75:
				fmt.Print("█")

			case v > 0.5:
				fmt.Print("▓")

			case v > 0.25:
				fmt.Print("▒")

			case v > 0.1:
				fmt.Print("░")

			default:
				fmt.Print(" ")
			}
		}

		fmt.Println()
		// fmt.Println("          ")
	}
}
