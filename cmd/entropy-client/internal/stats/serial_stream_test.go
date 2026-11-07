package stats

import (
	"math"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

func TestStreamSerialKnownSequences(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want float64
	}{
		{"increasing", []byte{1, 2, 3, 4}, 0.25},
		{"translated", []byte{101, 102, 103, 104}, 0.25},
		{"decreasing", []byte{4, 3, 2, 1}, 0.25},
		{"alternating", []byte{0, 255, 0, 255}, -0.75},
		{"constant", []byte{42, 42, 42, 42}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for chunk := 1; chunk <= len(tc.data); chunk++ {
				tester := NewStreamTester()
				for start := 0; start < len(tc.data); start += chunk {
					tester.Add(tc.data[start:min(start+chunk, len(tc.data))])
				}

				result := tester.Result()
				if math.Abs(result.SerialR-tc.want) > 1e-12 {
					t.Errorf("chunk size %d: got r=%g, want %g", chunk, result.SerialR, tc.want)
				}

				if math.IsNaN(result.SerialP) || result.SerialP < 0 || result.SerialP > 1 {
					t.Errorf("chunk size %d: invalid p-value %g", chunk, result.SerialP)
				}
			}
		})
	}
}

func TestStreamSerialMatchesBatchDiagnostics(t *testing.T) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte((i*73 + i/17) % 256)
	}

	want := diag.RunDiagnosticsWithMatrix(data, false)
	for _, chunk := range []int{1, 17, 1024, len(data)} {
		tester := NewStreamTester()
		for start := 0; start < len(data); start += chunk {
			tester.Add(data[start:min(start+chunk, len(data))])
		}

		got := tester.Result()
		if math.Abs(got.SerialR-want.SerialR) > 1e-12 || math.Abs(got.SerialP-want.SerialP) > 1e-12 {
			t.Errorf("chunk size %d: stream r=%g p=%g, batch r=%g p=%g",
				chunk, got.SerialR, got.SerialP, want.SerialR, want.SerialP)
		}
	}
}

func TestStreamSerialShortInputs(t *testing.T) {
	for size := range 4 {
		tester := NewStreamTester()
		tester.Add([]byte{1, 2, 3}[:size])

		result := tester.Result()
		if result.SerialR != 0 || result.SerialP != 1 {
			t.Errorf("%d bytes: got r=%g p=%g, want r=0 p=1", size, result.SerialR, result.SerialP)
		}
	}
}
