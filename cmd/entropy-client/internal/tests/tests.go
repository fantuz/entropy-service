package tests

import (
	"fmt"
	"io"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

const (
	Reset = "\033[0m"
	Red   = "\033[31m"
	Green = "\033[32m"
)

// PrintDiagnostics prints one raw Shannon estimate and one threshold assessment.
// The other statistics remain independent reference measurements.
func PrintDiagnostics(w io.Writer, result *diag.Diagnostics, threshold float64) {
	PrintDiagnosticsWithColor(w, result, threshold, true)
}

// PrintDiagnosticsWithColor supports an unstyled diagnostic assessment.
func PrintDiagnosticsWithColor(w io.Writer, result *diag.Diagnostics, threshold float64, colors bool) {
	fmt.Fprintln(w, "Sample diagnostics")
	fmt.Fprintln(w, "---------------------------")
	fmt.Fprintf(w, "sample bytes       : %d\n", result.N)
	fmt.Fprintf(w, "Shannon entropy    : %.6f bits/byte\n", result.Shannon)
	fmt.Fprintf(w, "Shannon threshold  : %.6f bits/byte\n", threshold)
	status := diag.ShannonAssessment(result, threshold)

	color := Green
	if status != diag.ShannonOK {
		color = Red
	}

	reset := Reset
	if !colors {
		color, reset = "", ""
	}

	fmt.Fprintf(w, "Shannon assessment : %s%s%s\n", color, status, reset)
	fmt.Fprintf(w, "chi-square         : %.3f (p=%.5f)\n", result.Chi2, result.Chi2P)
	fmt.Fprintf(w, "monobit p-value    : %.5f\n", result.MonobitP)
	fmt.Fprintf(w, "serial correlation : %.6f (p=%.5f)\n", result.SerialR, result.SerialP)
}
