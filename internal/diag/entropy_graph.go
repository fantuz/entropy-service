package diag

import "fmt"

type EntropyGraph struct {
	values []float64
	width  int
}

func NewEntropyGraph(width int) *EntropyGraph {

	return &EntropyGraph{
		values: make([]float64, 0, width),
		width:  width,
	}
}

func (g *EntropyGraph) Add(v float64) {

	if len(g.values) >= g.width {
		g.values = g.values[1:]
	}

	g.values = append(g.values, v)
}

func (g *EntropyGraph) Render() {

	//fmt.Print("\033[H\033[2J") // clear screen

	fmt.Println("\nEntropy monitor")
	fmt.Println("----------------")

	fmt.Print("                     ")
	for _, v := range g.values {

		level := int((v / 8.0) * 8)

		switch level {

		case 0, 1:
			fmt.Print("▁")

		case 2:
			fmt.Print("▂")

		case 3:
			fmt.Print("▃")

		case 4:
			fmt.Print("▄")

		case 5:
			fmt.Print("▅")

		case 6:
			fmt.Print("▆")

		case 7:
			fmt.Print("▇")

		default:
			fmt.Print("█")
		}
	}

	fmt.Println()
}
