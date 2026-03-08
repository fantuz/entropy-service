package diag

import (
	"fmt"
	//"math"
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

	return &Dashboard{
		width: width,
		rate:  NewRateMeter(),
	}
}

func push(series []float64, v float64, width int) []float64 {

	if len(series) >= width {
		series = series[1:]
	}

	return append(series, v)
}

func (d *Dashboard) Add(r Diagnostics) {

	d.entropy = push(d.entropy, r.Shannon, d.width)
	d.chiP = push(d.chiP, r.Chi2P, d.width)
	d.monoP = push(d.monoP, r.MonobitP, d.width)
	d.serial = push(d.serial, r.SerialR, d.width)
	d.rate.Update(r.N)
	//d.rate.Update(int(math.Round(d.rate.RateMbps())))
	//d.rate.Update(len(r.N))
}

func spark(v float64, min float64, max float64) rune {

	if v < min {
		v = min
	}

	if v > max {
		v = max
	}

	n := int((v - min) / (max - min) * 7)

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

func renderSeries(series []float64, min float64, max float64) string {

	out := ""

	for _, v := range series {
		out += string(spark(v, min, max))
	}

	return out
}

func (d *Dashboard) Render() {

	//fmt.Print("\033[H\033[2J")

	fmt.Println("Entropy Health Monitor")
	fmt.Println("----------------------")
	fmt.Printf(
		"Entropy rate : %.2f Mbit/s\n",
		d.rate.RateMbps(),
	)
	fmt.Printf(
		"Entropy      %s\n",
		renderSeries(d.entropy, 7.5, 8.0),
	)

	fmt.Printf(
		"Chi-square   %s\n",
		renderSeries(d.chiP, 0.0, 1.0),
	)

	fmt.Printf(
		"Monobit      %s\n",
		renderSeries(d.monoP, 0.0, 1.0),
	)

	fmt.Printf(
		"Serial corr  %s\n",
		renderSeries(d.serial, -0.1, 0.1),
	)
	//fmt.Println("----------------------")
}

