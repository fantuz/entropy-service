package tests

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

func TestPrintDiagnosticsHasOneShannonMeasurementAndAssessment(t *testing.T) {
	result := diag.RunDiagnosticsWithMatrix([]byte{0, 1, 0, 1}, false)

	var out bytes.Buffer

	PrintDiagnostics(&out, &result, 1.1)

	text := out.String()
	for _, want := range []string{"Shannon entropy    : 1.000000 bits/byte", "Shannon threshold  : 1.100000 bits/byte", "Shannon assessment : " + Red + "WARN", "chi-square", "monobit", "serial correlation"} {
		if strings.Count(text, want) != 1 {
			t.Errorf("wanted one %q in %q", want, text)
		}
	}

	if strings.Contains(text, "result ") || strings.Contains(text, "Status") || strings.Contains(text, "OK") {
		t.Fatalf("conflicting aggregate status in %q", text)
	}
}

func TestPlainDiagnosticsKeepAssessmentWithoutEscapes(t *testing.T) {
	result := diag.RunDiagnosticsWithMatrix([]byte{0, 1, 0, 1}, false)

	for _, threshold := range []float64{0, 2} {
		var out bytes.Buffer
		PrintDiagnosticsWithColor(&out, &result, threshold, false)

		if strings.ContainsRune(out.String(), '\x1b') || !strings.Contains(out.String(), "Shannon assessment : "+diag.ShannonAssessment(&result, threshold)) {
			t.Fatalf("plain diagnostics lost their assessment or contain ANSI: %q", out.String())
		}
	}
}
