package stats

import "math"

func Serial(data []byte) Result {

	if len(data) < 2 {
		return Result{Name: "serial", Passed: false}
	}

	counts := make([]int, 256)

	for i := 0; i < len(data)-1; i++ {
		v := int(data[i])
		counts[v]++
	}

	expected := float64(len(data)-1) / 256
	chi := 0.0

	for _, c := range counts {
		diff := float64(c) - expected
		chi += diff * diff / expected
	}

	p := math.Exp(-chi / 2)

	return Result{
		Name:      "serial",
		Statistic: chi,
		PValue:    p,
		Passed:    p > 0.01,
	}
}

//r = covariance / variance

func SerialCorrelation(data []byte) Result {

	n := len(data)

	var sum float64
	var sumSq float64
	var sumProd float64

	for i := 0; i < n-1; i++ {

		x := float64(data[i])
		y := float64(data[i+1])

		sum += x
		sumSq += x * x
		sumProd += x * y
	}

	mean := sum / float64(n)

	variance := sumSq/float64(n) - mean*mean

	cov := sumProd/float64(n-1) - mean*mean

	r := cov / variance

	return Result{
		Statistic: r,
		Passed:    math.Abs(r) < 0.05,
	}
}
