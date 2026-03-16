package stats

// Result represents the outcome of a statistical test.
type Result struct {
	Name      string
	Statistic float64
	PValue    float64
	Passed    bool
}
