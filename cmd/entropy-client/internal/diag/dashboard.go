package diag

import (
	"fmt"
	"io"
	"os"
	"strings"
)

type Dashboard struct {
	width int

	entropy []float64
	chiP    []float64
	monoP   []float64
	serial  []float64

	rate *RateMeter
}

func NewDashboard(width int) *Dashboard {
	width = max(1, min(width, 256))

	return &Dashboard{
		width:   width,
		rate:    NewRateMeter(),
		entropy: make([]float64, 0, width), chiP: make([]float64, 0, width),
		monoP: make([]float64, 0, width), serial: make([]float64, 0, width),
	}
}

func push(series []float64, v float64, width int) []float64 {
	if len(series) >= width {
		copy(series, series[1:])
		series[len(series)-1] = v

		return series
	}

	return append(series, v)
}

func (d *Dashboard) Add(r Diagnostics) {
	d.entropy = push(d.entropy, r.Shannon, d.width)
	d.chiP = push(d.chiP, r.Chi2P, d.width)
	d.monoP = push(d.monoP, r.MonobitP, d.width)
	d.serial = push(d.serial, r.SerialR, d.width)

	d.rate.Update(r.N)
	// d.rate.Update(int(math.Round(d.rate.RateMbps())))
	// d.rate.Update(int(r.Rate))
	// d.rate.Update(len(r.N))
}

func spark(v float64, lo float64, hi float64) rune {
	if v < lo {
		v = lo
	}

	if v > hi {
		v = hi
	}

	n := int((v - lo) / (hi - lo) * 7)

	switch n {
	case 0:
		return '▁'
	case 1:
		return '▂'
	case 2:
		return '▃'
	case 3:
		return '▄'
	case 4:
		return '▅'
	case 5:
		return '▆'
	case 6:
		return '▇'
	default:
		return '█'
	}
}

func renderSeries(series []float64, lo float64, hi float64) string {
	var out strings.Builder

	for _, v := range series {
		out.WriteRune(spark(v, lo, hi))
	}

	return out.String()
}

func (d *Dashboard) Render() {
	d.RenderTo(os.Stdout)
}

func (d *Dashboard) RenderTo(out io.Writer) {
	d.RenderWidth(out, d.width)
}

func (d *Dashboard) RenderWidth(out io.Writer, cells int) {
	cells = max(1, min(cells, d.width))
	recent := func(values []float64) []float64 { return values[max(0, len(values)-cells):] }
	total := ClientRateMeter.Total()
	rate := ClientRateMeter.RateMiB()

	fmt.Fprintln(out, "\nSampled statistics history")
	fmt.Fprintln(out, "----------------------------")
	fmt.Fprintf(out, "Total received     : %d MiB\n", total/1024/1024)
	fmt.Fprintf(out, "Transfer Rate      : %.3f MiB/s\n", rate)

	fmt.Fprintf(out,
		"Analysed Rate      : %.3f MiB/s\n",
		d.rate.RateMiB(),
	)

	fmt.Fprintf(out,
		"Shannon entropy      %s\n",
		renderSeries(recent(d.entropy), 7.995, 8.0),
	)

	fmt.Fprintf(out,
		"Chi-square           %s\n",
		renderSeries(recent(d.chiP), 0.0, 1.0),
	)

	fmt.Fprintf(out,
		"Monobit              %s\n",
		renderSeries(recent(d.monoP), 0.0, 1.0),
	)

	fmt.Fprintf(out,
		"Serial correlation   %s\n",
		renderSeries(recent(d.serial), -0.02, 0.02), // 0.1
	)
	fmt.Fprintln(out, "----------------------------")
}
