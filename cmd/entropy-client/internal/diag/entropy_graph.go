package diag

import (
	"fmt"
	"io"
	"os"
)

type EntropyGraph struct {
	values []float64
	width  int
}

func NewEntropyGraph(width int) *EntropyGraph {
	width = max(1, min(width, 256))

	return &EntropyGraph{
		values: make([]float64, 0, width),
		width:  width,
	}
}

func (g *EntropyGraph) Add(v float64) {
	if len(g.values) >= g.width {
		copy(g.values, g.values[1:])
		g.values[len(g.values)-1] = v

		return
	}

	g.values = append(g.values, v)
}

func (g *EntropyGraph) Render() {
	g.RenderTo(os.Stdout)
}

func (g *EntropyGraph) RenderTo(out io.Writer) { g.RenderWidth(out, g.width) }

func (g *EntropyGraph) RenderWidth(out io.Writer, cells int) {
	fmt.Fprintln(out, "\nShannon entropy history (bits/byte, 0..8)")
	fmt.Fprintln(out, "----------------")

	for _, v := range g.values[max(0, len(g.values)-max(1, cells)):] {
		level := int((v / 8.0) * 8)

		switch level {
		case 0, 1:
			fmt.Fprint(out, "▁")

		case 2:
			fmt.Fprint(out, "▂")

		case 3:
			fmt.Fprint(out, "▃")

		case 4:
			fmt.Fprint(out, "▄")

		case 5:
			fmt.Fprint(out, "▅")

		case 6:
			fmt.Fprint(out, "▆")

		case 7:
			fmt.Fprint(out, "▇")

		default:
			fmt.Fprint(out, "█")
		}
	}

	fmt.Fprintln(out)
}
