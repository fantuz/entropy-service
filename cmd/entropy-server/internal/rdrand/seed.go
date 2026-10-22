package rdrand

import (
	"errors"
	"fmt"
	"io"
)

var (
	ErrSeedUnsupported = errors.New("RDSEED is not supported on this CPU or build")
	ErrSeedUnavailable = errors.New("RDSEED failed after one attempt and ten retries")
)

// RDSEEDAvailable reports whether this build and CPU support RDSEED.
func RDSEEDAvailable() bool {
	return seedAvailable
}

// SeedUint64 returns a hardware seed word, checking support and bounded retries.
// As with RDRAND, a successful zero is valid.
func SeedUint64() (uint64, error) {
	if !RDSEEDAvailable() {
		return 0, ErrSeedUnsupported
	}

	value, ok := readSeedUint64()
	if !ok {
		return 0, ErrSeedUnavailable
	}

	return value, nil
}

// NewCPUMixer prefers RDSEED, falling back to RDRAND for each sample. If neither
// succeeds, Mixer keeps the configured source bytes and reports the first
// failure through onFailure. Unsupported CPUs/builds also keep those bytes.
func NewCPUMixer(source io.Reader, onFailure func(error)) *Mixer {
	return &Mixer{source: source, next: cpuUint64, onFailure: onFailure}
}

func cpuUint64() (uint64, error) {
	return seedOrRandom(SeedUint64, Uint64)
}

func seedOrRandom(seed, random func() (uint64, error)) (uint64, error) {
	value, seedErr := seed()
	if seedErr == nil {
		return value, nil
	}

	value, err := random()
	if err == nil {
		return value, nil
	}

	return 0, fmt.Errorf("RDSEED: %w; RDRAND: %w", seedErr, err)
}
