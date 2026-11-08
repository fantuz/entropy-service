package diag

import (
	"bytes"
	"testing"
	"time"
)

func TestOptionalMatrixPreservesOtherStatistics(t *testing.T) {
	data := bytes.Repeat([]byte{0, 1, 2, 3, 127, 128, 254, 255}, 1024)
	full := RunDiagnostics(data)

	light := RunDiagnosticsWithMatrix(data, false)
	if full.TMatrix.Total != len(data)-1 || light.TMatrix.Total != 0 {
		t.Fatal("matrix selection did not control matrix computation")
	}

	if light.N != full.N || light.Histogram != full.Histogram || light.Shannon != full.Shannon ||
		light.Chi2 != full.Chi2 || light.Chi2P != full.Chi2P || light.MonobitP != full.MonobitP ||
		light.SerialR != full.SerialR || light.SerialP != full.SerialP {
		t.Fatal("omitting matrix changed unrelated statistics")
	}
}

func TestRateUnitsAndReset(t *testing.T) {
	r := NewRateMeter()

	r.rate = 8 * 1024 * 1024
	if r.RateMiB() != 1 || r.RateMbps() != 8.388608 {
		t.Fatal("binary byte and decimal bit units were mixed")
	}

	r.Update(100)
	r.Reset()

	if r.Total() != 0 || r.Bytes() != 0 || r.RateMiB() != 0 || time.Since(r.last) > time.Second {
		t.Fatal("startup counters leaked into the monitoring window")
	}
}

func TestHighRateDoesNotOverflowOn32BitHosts(t *testing.T) {
	r := NewRateMeter()
	r.last = time.Now().Add(-time.Second)
	r.Update(320 << 20)

	if r.RateMiB() <= 0 {
		t.Fatal("320 MiB/s overflowed the bit-rate calculation")
	}
}
