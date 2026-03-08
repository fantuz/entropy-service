package diag

import "fmt"

// perfect RNG -> 1 / 64 ≈ 0.015625

type TransitionMatrix struct {
	Matrix [32][32]int
	Total  int
}

func BuildTransitionMatrix(data []byte) TransitionMatrix {

	var tm TransitionMatrix

	if len(data) < 2 {
		return tm
	}

	for i := 0; i < len(data)-1; i++ {

		a := int(data[i]) / 8
		b := int(data[i+1]) / 8

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

	max := 0

	for i := 0; i < 32; i++ {
		for j := 0; j < 32; j++ {
			if tm.Matrix[i][j] > max {
				max = tm.Matrix[i][j]
			}
		}
	}

	if max == 0 {
		max = 1
	}

	fmt.Println("\nTransition Matrix (32x32)")
	fmt.Println("--------------------------")

	for i := 0; i < 32; i++ {

		for j := 0; j < 32; j++ {

			v := float64(tm.Matrix[i][j]) / float64(max)

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
	}
}

