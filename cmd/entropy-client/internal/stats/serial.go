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
		Pass: math.Abs(r) < 0.05,
	}
}
