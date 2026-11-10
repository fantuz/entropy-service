package diag

import (
	"strings"
	"testing"
)

func TestDashboardKeepsRecentSampleHistory(t *testing.T) {
	dashboard := NewDashboard(3)
	for i := range 10 {
		dashboard.Add(Diagnostics{N: 64, Shannon: float64(i), Chi2P: float64(i) / 10, MonobitP: .5, SerialR: 0})
	}

	if len(dashboard.entropy) != 3 || dashboard.entropy[0] != 7 || dashboard.entropy[2] != 9 || dashboard.rate.Total() != 640 {
		t.Fatal("dashboard lost sample ordering, bounded history or analysed-byte accounting")
	}

	var out strings.Builder
	dashboard.RenderTo(&out)

	for _, label := range []string{"Sampled statistics history", "Total received", "Transfer Rate", "Analysed Rate", "Shannon entropy", "Chi-square", "Monobit", "Serial correlation"} {
		if strings.Count(out.String(), label) != 1 {
			t.Errorf("missing or duplicate %q in %q", label, out.String())
		}
	}
}

func TestSparklineClampsAndPreservesEightLevels(t *testing.T) {
	values := []float64{-1, 0, 1, 2, 3, 4, 5, 6, 7, 100}
	if got := renderSeries(values, 0, 7); got != "▁▁▂▃▄▅▆▇██" {
		t.Fatalf("sparkline levels/clipping = %q", got)
	}
}

func TestEntropyGraphRetainsLatestValues(t *testing.T) {
	graph := NewEntropyGraph(8)
	for i := range 10 {
		graph.Add(float64(i))
	}

	if len(graph.values) != 8 || graph.values[0] != 2 || graph.values[7] != 9 {
		t.Fatal("graph did not evict only the oldest values")
	}

	graph = NewEntropyGraph(9)
	for i := range 9 {
		graph.Add(float64(i))
	}

	var out strings.Builder
	graph.RenderTo(&out)

	if !strings.Contains(out.String(), "bits/byte, 0..8") || !strings.HasSuffix(out.String(), "▁▁▂▃▄▅▆▇█\n") {
		t.Fatalf("entropy graph scale/levels changed: %q", out.String())
	}
}

func TestHistogramCountsPreserveEightByteBuckets(t *testing.T) {
	var histogram [256]int

	histogram[0], histogram[7], histogram[8], histogram[255] = 1, 2, 3, 1

	var out strings.Builder
	PrintHistogramCountsTo(&out, histogram)

	text := out.String()
	if !strings.Contains(text, "00-07 | "+strings.Repeat("█", 50)) ||
		!strings.Contains(text, "08-0f | "+strings.Repeat("█", 50)) ||
		!strings.Contains(text, "f8-ff | "+strings.Repeat("█", 16)) ||
		strings.Count(text, " | ") != 32 {
		t.Fatalf("histogram boundary or scaling error: %q", text)
	}

	out.Reset()
	PrintHistogramCountsTo(&out, [256]int{})

	if strings.ContainsRune(out.String(), '█') || strings.Count(out.String(), " | ") != 32 {
		t.Fatal("empty histogram should keep all buckets empty")
	}
}

func TestTransitionHeatmapHandlesEmptyAndPopulatedSamples(t *testing.T) {
	matrix := BuildTransitionMatrix([]byte{0, 4, 0, 4})
	if matrix.Probability(0, 1) != 2.0/3 || matrix.Probability(1, 0) != 1.0/3 {
		t.Fatal("transition probability used the wrong denominator")
	}

	var out strings.Builder
	matrix.PrintHeatmapTo(&out)

	if !strings.Contains(out.String(), "Transition Matrix (64x64)") || !strings.ContainsRune(out.String(), '█') || !strings.ContainsRune(out.String(), '▒') {
		t.Fatalf("heatmap omitted transition levels: %q", out.String())
	}

	matrix = TransitionMatrix{}

	out.Reset()
	matrix.PrintHeatmapTo(&out)

	if matrix.Probability(0, 0) != 0 || strings.ContainsAny(out.String(), "█▓▒░") {
		t.Fatal("empty matrix produced probabilities or painted transitions")
	}
}
