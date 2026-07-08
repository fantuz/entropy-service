package stats

// RunAll executes a small battery on a single byte slice and returns results.
func RunAll(data []byte) []Result {
	results := []Result{
		Monobit(data),
		ChiSquare(data),
		Serial(data),
	}

	return results
}
