// χ² = Σ (O - E)² / E

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

	return Result{
		Statistic: chi2,
		Pass: pass,
	}
}
