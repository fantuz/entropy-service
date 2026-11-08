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
	for _, want := range []string{"Shannon entropy    : 1.00000 bits/byte", "Shannon threshold  : 1.10000 bits/byte", "Shannon assessment : " + Red + "WARN", "chi-square", "monobit", "serial correlation"} {
		if strings.Count(text, want) != 1 {
			t.Errorf("wanted one %q in %q", want, text)
		}
	}

	if strings.Contains(text, "result ") || strings.Contains(text, "Status") || strings.Contains(text, "OK") {
		t.Fatalf("conflicting aggregate status in %q", text)
	}
}
