package diag

import (
	"bytes"
	"testing"
)

func TestShannonAssessmentBoundaryAndNoSample(t *testing.T) {
	result := RunDiagnosticsWithMatrix([]byte{0, 1, 0, 1}, false)
	if result.Shannon != 1 {
		t.Fatalf("binary sample Shannon = %g, want 1 bit/byte", result.Shannon)
	}

	for _, tc := range []struct {
		threshold float64
		want      string
	}{
		{0, ShannonOK},
		{1, ShannonOK},
		{1.001, ShannonWarn},
		{8, ShannonWarn},
	} {
		if got := ShannonAssessment(&result, tc.threshold); got != tc.want {
			t.Errorf("threshold=%g: %s, want %s", tc.threshold, got, tc.want)
		}
	}

	if result.Shannon != 1 {
		t.Fatal("threshold comparison modified the raw estimate")
	}

	empty := RunDiagnosticsWithMatrix(nil, false)
	if got := ShannonAssessment(&empty, 0); got != ShannonNoSample {
		t.Fatalf("empty input = %s, want NO_SAMPLE even at threshold 0", got)
	}
}

func TestShannonAssessmentIsNotAnIndependenceTest(t *testing.T) {
	// An ordered repetition has a perfectly uniform histogram but predictable
	// successive bytes. The Shannon badge must not imply all tests passed.
	cycle := make([]byte, 256)
	for i := range cycle {
		cycle[i] = byte(i)
	}

	result := RunDiagnosticsWithMatrix(bytes.Repeat(cycle, 16), false)
	if result.Shannon != 8 || ShannonAssessment(&result, 8) != ShannonOK {
		t.Fatalf("balanced byte counts should give H=8 and OK: H=%g", result.Shannon)
	}

	if result.SerialR < 0.9 || result.SerialP > 0.001 {
		t.Fatalf("raw serial statistic lost predictable ordering: r=%g p=%g", result.SerialR, result.SerialP)
	}
}
