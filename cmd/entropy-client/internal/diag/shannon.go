package diag

const (
	DefaultShannonThreshold = 7.999
	ShannonOK               = "OK"
	ShannonWarn             = "WARN"
	ShannonNoSample         = "NO_SAMPLE"
)

// ShannonAssessment compares the unmodified empirical bits/byte estimate with
// an operator-selected minimum. It is not a weighted score or a bit rate.
func ShannonAssessment(result *Diagnostics, threshold float64) string {
	if result.N == 0 {
		return ShannonNoSample
	}

	if result.Shannon < threshold {
		return ShannonWarn
	}

	return ShannonOK
}
