//S = (#1 − #0)

func Monobit(data []byte) Result {

	var ones int

	for _, b := range data {
		ones += bits.OnesCount8(b)
	}

	totalBits := len(data) * 8
	zeros := totalBits - ones

	s := float64(ones - zeros)

	stat := math.Abs(s) / math.Sqrt(float64(totalBits))

	p := math.Erfc(stat / math.Sqrt2)

	return Result{
		Statistic: stat,
		PValue: p,
		Pass: p > 0.01,
	}
}
