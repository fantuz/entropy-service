//go:build !experimental_barzakh

package drbg

import (
	"strings"
	"testing"
)

func TestBarzakhExcludedFromNormalBuild(t *testing.T) {
	if _, err := ParseAlgorithm(string(Barzakh521)); err == nil || !strings.Contains(err.Error(), "-tags=experimental_barzakh") {
		t.Fatalf("normal build must reject the experimental backend with build instructions: %v", err)
	}

	baseline := ActiveInstances()
	if _, err := NewDRBGWithAlgorithm(testSeed(), Barzakh521); err == nil || ActiveInstances() != baseline {
		t.Fatal("normal build constructed an experimental generator or leaked accounting")
	}

	if stream, err := newBarzakhStream(&[32]byte{}, &[16]byte{}); stream != nil || err == nil {
		t.Fatal("normal build must not provide an experimental stream")
	}
}
