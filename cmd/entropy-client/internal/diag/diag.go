package diag

import (
	"math"
	"math/bits"
	"sync/atomic"
)

// Diagnostics is the summary returned by RunDiagnostics.
type Diagnostics struct {
	N        int
	Chi2     float64
	Chi2P    float64
	Shannon  float64
	MonobitP float64
	SerialR  float64
	SerialP  float64
	Rate     *RateMeter
	TMatrix  TransitionMatrix
	Pass     bool
	Notes    string
}

var (
	BytesFetched  uint64
	httpCRequests uint64
	wsCRequests   uint64
	Timer         uint64
)

// RunDiagnostics computes a small battery of tests on the provided bytes.
// It is intentionally small and self-contained so it can be called from a worker or CLI.
func RunDiagnostics(data []byte) Diagnostics {
	n := len(data)
	if n == 0 {
		return Diagnostics{N: 0, Pass: false, Notes: "empty input"}
	}

	hist := computeHistogram(data)
	chi2 := chiSquare(hist, n)
	chi2p := chi2PValue(chi2, 256-1)
	tm := BuildTransitionMatrix(data)

	shannon := shannonEntropy(hist, n)
	monop := monobitPvalue(data)

	serialR, serialP := serialCorrelation(data)

	// simple pass rule (thresholds to be tuned)
	pass := true
	if monop < 0.01 || chi2p < 0.01 || shannon < 7.9 || math.Abs(serialR) > 0.05 {
		pass = false
	}

	atomic.AddUint64(&BytesFetched, uint64(n))
	//atomic.AddUint64(&httpCRequests, 1)

	rate := NewRateMeter()
	//rate.Update(len(data))

	return Diagnostics{
		N:        n,
		//Update(len(data)),
		Chi2:     chi2,
		Chi2P:    chi2p,
		Shannon:  shannon,
		MonobitP: monop,
		SerialR:  serialR,
		SerialP:  serialP,
		TMatrix:  tm,
		Pass:     pass,
		Rate:     rate,
		//Notes:    "ciao",
	}
}

// ---------------- helpers ----------------

func computeHistogram(data []byte) [256]int {
	var h [256]int
	for _, b := range data {
		h[b]++
	}
	return h
}

func chiSquare(hist [256]int, n int) float64 {
	if n == 0 {
		return 0
	}
	expected := float64(n) / 256.0
	var chi2 float64
	for i := 0; i < 256; i++ {
		diff := float64(hist[i]) - expected
		chi2 += (diff * diff) / expected
	}
	return chi2
}

// shannonEntropy computes H = -sum p*log2(p)
func shannonEntropy(hist [256]int, n int) float64 {
	if n == 0 {
		return 0
	}
	total := float64(n)
	var H float64
	for i := 0; i < 256; i++ {
		c := hist[i]
		if c == 0 {
			continue
		}
		p := float64(c) / total
		H -= p * (math.Log2(p))
	}
	return H
}

// monobit p-value using erfc (NIST monobit)
func monobitPvalue(data []byte) float64 {
	ones := 0
	for _, b := range data {
		ones += bits.OnesCount8(b)
	}
	totalBits := len(data) * 8
	if totalBits == 0 {
		return 1.0
	}
	s := float64(ones - (totalBits - ones))
	// p = erfc(|s|/sqrt(2n))
	return math.Erfc(math.Abs(s) / math.Sqrt(2*float64(totalBits)))
}

// serialCorrelation computes lag-1 autocorrelation (bytes) and approximate p-value
func serialCorrelation(data []byte) (r float64, pval float64) {
	N := len(data)
	if N < 4 {
		return 0, 1.0
	}
	var sum float64
	for i := 0; i < N; i++ {
		sum += float64(data[i])
	}
	mean := sum / float64(N)

	var num float64
	var den float64
	for i := 0; i < N-1; i++ {
		a := float64(data[i]) - mean
		b := float64(data[i+1]) - mean
		num += a * b
		den += (float64(data[i]) - mean) * (float64(data[i]) - mean)
	}
	// include last in den for stability
	den += (float64(data[N-1]) - mean) * (float64(data[N-1]) - mean)
	if den == 0 {
		return 0, 1.0
	}
	r = num / den

	// Fisher z-transform for approximate p-value
	if r >= 1.0 {
		r = 0.999999
	}
	if r <= -1.0 {
		r = -0.999999
	}
	zprime := 0.5 * math.Log((1+r)/(1-r))
	sigma := 1.0 / math.Sqrt(float64(N-3))
	Z := zprime / sigma
	pval = 2 * (1 - normalCDF(math.Abs(Z)))
	return r, pval
}

// ---------------- statistical helpers ----------------

// Wilson–Hilferty approximation to get p-value for chi2
func chi2PValue(chi2 float64, df int) float64 {
	if df <= 0 {
		return math.NaN()
	}
	term := math.Pow(chi2/float64(df), 1.0/3.0)
	z := (term - (1.0 - 2.0/(9.0*float64(df)))) / math.Sqrt(2.0/(9.0*float64(df)))
	// right-tail p-value from normal approx
	return 1 - normalCDF(z)
}

// standard normal CDF using erf
func normalCDF(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}
