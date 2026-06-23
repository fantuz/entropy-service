package stats

import (
	"context"
	"io"
	"math"
	"math/bits"
	//"time"
)

// StreamResult summarizes computed live metrics.
type StreamResult struct {
	TotalBytes uint64
	Shannon    float64
	Chi2       float64
	Chi2P      float64
	MonobitP   float64
	SerialR    float64
	SerialP    float64
}

// StreamTester maintains incremental state while streaming bytes.
type StreamTester struct {
	Total uint64

	// histogram counts (byte-level)
	Hist [256]uint64

	// bit counts
	BitOnes uint64

	// for serial lag-1 correlation
	sum      float64 // sum of x
	sumSq    float64 // sum of x^2
	sumProd  float64 // sum of x_i * x_{i+1}
	prev     byte
	havePrev bool
	pairs    uint64 // number of adjacent pairs counted
}

// NewStreamTester returns an initialized tester.
func NewStreamTester() *StreamTester {
	return &StreamTester{}
}

// Add processes a chunk of bytes, updating incremental counters.
// Safe to call repeatedly as data arrives.
func (t *StreamTester) Add(b []byte) {
	n := len(b)
	if n == 0 {
		return
	}

	// Update histogram and bit counts
	for i := 0; i < n; i++ {
		v := b[i]
		t.Hist[v]++
		t.BitOnes += uint64(bits.OnesCount8(v))

		// serial accumulators
		x := float64(v)
		t.sum += x
		t.sumSq += x * x

		if t.havePrev {
			t.sumProd += float64(t.prev) * x
			t.pairs++
		}
		t.prev = v
		t.havePrev = true
	}

	t.Total += uint64(n)
}

// computeShannon computes Shannon entropy from current histogram.
func (t *StreamTester) computeShannon() float64 {
	if t.Total == 0 {
		return 0
	}
	total := float64(t.Total)
	var H float64
	for i := 0; i < 256; i++ {
		c := t.Hist[i]
		if c == 0 {
			continue
		}
		p := float64(c) / total
		H -= p * math.Log2(p)
	}
	return H
}

// computeChi2 computes chi-square statistic and p-value (Wilson–Hilferty approx).
func (t *StreamTester) computeChi2() (chi2 float64, pval float64) {
	if t.Total == 0 {
		return 0, 1.0
	}
	expected := float64(t.Total) / 256.0
	for i := 0; i < 256; i++ {
		diff := float64(t.Hist[i]) - expected
		chi2 += diff * diff / expected
	}
	// df = 255
	p := chi2PValue(chi2, 256-1)
	return chi2, p
}

// computeMonobit returns monobit p-value.
func (t *StreamTester) computeMonobit() float64 {
	totalBits := float64(t.Total) * 8.0
	if totalBits == 0 {
		return 1.0
	}
	ones := float64(t.BitOnes)
	zeros := totalBits - ones
	S := ones - zeros
	p := erfc(math.Abs(S) / math.Sqrt(2*totalBits))
	return p
}

// computeSerial computes lag-1 autocorrelation r and approximate p-value.
func (t *StreamTester) computeSerial() (r float64, pval float64) {
	N := t.Total
	if N < 3 || t.pairs == 0 {
		return 0, 1.0
	}

	// Use sums we collected: sum = Σ x_i, sumSq = Σ x_i^2, sumProd = Σ x_i * x_{i+1}
	// We compute r ≈ ( Σ x_i x_{i+1} - (Σ x_i)^2 / N ) / ( Σ x_i^2 - (Σ x_i)^2 / N )
	total := float64(N)
	//nPairs := float64(t.pairs)

	numerator := t.sumProd - (t.sum*t.sum)/total
	denominator := t.sumSq - (t.sum*t.sum)/total
	if denominator == 0 {
		return 0, 1.0
	}
	r = numerator / denominator
	// fisher z-transform for p-value approximate:
	if r >= 1.0 {
		r = 0.999999
	}
	if r <= -1.0 {
		r = -0.999999
	}
	zprime := 0.5 * math.Log((1+r)/(1-r))
	sigma := 1.0 / math.Sqrt(total-3.0)
	Z := zprime / sigma
	pval = 2 * (1 - normalCDF(math.Abs(Z)))
	return r, pval
}

// Result returns current StreamResult computed from the aggregated state.
func (t *StreamTester) Result() StreamResult {
	shannon := t.computeShannon()
	chi2, chi2p := t.computeChi2()
	mono := t.computeMonobit()
	serialR, serialP := t.computeSerial()

	return StreamResult{
		TotalBytes: t.Total,
		Shannon:    shannon,
		Chi2:       chi2,
		Chi2P:      chi2p,
		MonobitP:   mono,
		SerialR:    serialR,
		SerialP:    serialP,
	}
}

// RunFromReader reads from any io.Reader in fixed chunk sizes and calls cb periodically.
// ctx cancels the operation; cb is invoked after each 'callbackInterval' bytes processed (approx).
func RunFromReader(ctx context.Context, r io.Reader, chunkSize int, callbackInterval uint64, cb func(StreamResult)) error {
	buf := make([]byte, chunkSize)
	tester := NewStreamTester()
	var nextCallback uint64 = callbackInterval
	if nextCallback == 0 {
		nextCallback = uint64(chunkSize) // default: every chunk
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			tester.Add(buf[:n])
			if tester.Total >= nextCallback {
				cb(tester.Result())
				nextCallback += callbackInterval
			}
		}
		if err != nil {
			if err == io.EOF {
				// final callback
				cb(tester.Result())
				return nil
			}
			return err
		}
	}
}

// ---------------- statistical helpers ----------------

func normalCDF(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// complementary error function (Abramowitz & Stegun approximation)
func erfc(x float64) float64 {
	// using math.Erfc is available starting Go 1.10; use it if you prefer:
	return math.Erfc(x)
}

// chi2 p-value approximate (Wilson–Hilferty)
func chi2PValue(chi2 float64, df int) float64 {
	if df <= 0 {
		return math.NaN()
	}
	term := math.Pow(chi2/float64(df), 1.0/3.0)
	z := (term - (1.0 - 2.0/(9.0*float64(df)))) / math.Sqrt(2.0/(9.0*float64(df)))
	return 1 - normalCDF(z)
}
