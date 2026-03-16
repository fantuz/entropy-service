package stats

//import "math"

/*
func ChiSquare(data []byte) Result {

	var counts [256]int

	for _, b := range data {
		counts[b]++
	}

	expected := float64(len(data)) / 256

	chi := 0.0

	for i := 0; i < 256; i++ {
		diff := float64(counts[i]) - expected
		chi += diff * diff / expected
	}

	p := math.Exp(-chi / 2)

	return Result{
		Name:      "chi-square",
		Statistic: chi,
		PValue:    p,
		Passed:    p > 0.01,
	}
}
*/


// χ² = Σ (O - E)² / E

/*
type Result struct {
	Statistic float64
	Pass      float64
}
*/

//func ChiSquare(data []byte)
func ChiSquare(data []byte) Result {

	var hist [256]int

	for _, b := range data {
		hist[b]++
	}

	n := float64(len(data))
	expected := n / 256

	var chi2 float64

	for _, o := range hist {

		diff := float64(o) - expected
		chi2 += (diff * diff) / expected
	}

	pass := chi2 < 293.24 // approx threshold

	return Result {
		Name:      "chi-square",
		Statistic: chi2,
		//PValue:    p,
		Passed: pass,
	}
}
