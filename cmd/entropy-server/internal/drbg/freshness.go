package drbg

import (
	"errors"
	"fmt"
	"io"
	"time"
)

type ReseedFailurePolicy string

const (
	ReseedStop     ReseedFailurePolicy = "stop"
	ReseedContinue ReseedFailurePolicy = "continue"
	SeedFresh                          = "fresh"
	SeedFailed                         = "failed"
	SeedStale                          = "stale"
)

// ErrSeedUnavailable means the configured serving freshness policy denies output.
// It does not assert that old generator state is compromised.
var ErrSeedUnavailable = errors.New("generation paused: seed source failed or last successful seed is too old")

func ValidateReseedFailurePolicy(policy ReseedFailurePolicy) error {
	if policy != ReseedStop && policy != ReseedContinue {
		return fmt.Errorf("on-reseed-failure must be stop or continue, got %q", policy)
	}

	return nil
}

// ConfigureReseedPolicy sets serving policy before exposing the root to clients.
// Bare DRBG constructors retain their deterministic, unrestricted read behavior;
// the server explicitly selects stop by default. Children follow the serving root.
func (d *DRBG) ConfigureReseedPolicy(policy ReseedFailurePolicy, maxAge time.Duration) error {
	if err := ValidateReseedFailurePolicy(policy); err != nil {
		return err
	}

	if maxAge <= 0 {
		return errors.New("maximum reseed age must be positive")
	}

	d.policyMu.Lock()
	defer d.policyMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return io.ErrClosedPipe
	}

	d.failurePolicy = policy
	d.maxReseedAge = maxAge
	d.notifyGenerationLocked()

	return nil
}

// SeedState distinguishes a reported failure from age expiry (including a hung
// source). A failure remains unresolved until a successful reseed clears it.
func (meta Metadata) SeedState(maxAge time.Duration) string {
	switch {
	case meta.LastReseedError != "":
		return SeedFailed
	case meta.ReseedAge > maxAge:
		return SeedStale
	default:
		return SeedFresh
	}
}

// CheckGeneration is an admission check, not a reservation. Every root/child
// Read repeats the check under the root's lock, independently of HTTP routing.
func (d *DRBG) CheckGeneration() error {
	d.policyMu.RLock()
	defer d.policyMu.RUnlock()

	return d.generationErrorLocked(time.Now())
}

// WatchGeneration snapshots a change notification, the freshness deadline and
// the current decision under one lock. A zero deadline disables the age timer.
// Waiting on source I/O never holds this lock, so stale gating still works when
// a reseed read blocks indefinitely.
func (d *DRBG) WatchGeneration() (<-chan struct{}, time.Time, error) {
	d.policyMu.RLock()
	defer d.policyMu.RUnlock()

	var deadline time.Time
	if d.failurePolicy == ReseedStop {
		deadline = d.Reseeded.Add(d.maxReseedAge)
	}

	return d.generationChanged, deadline, d.generationErrorLocked(time.Now())
}

func (d *DRBG) generationErrorLocked(now time.Time) error {
	if d.closed {
		return io.ErrClosedPipe
	}

	if d.failurePolicy == ReseedStop && (d.lastReseedError != "" || now.Sub(d.Reseeded) > d.maxReseedAge) {
		return ErrSeedUnavailable
	}

	return nil
}

func (d *DRBG) notifyGenerationLocked() {
	if d.generationChanged != nil {
		close(d.generationChanged)
	}

	// Only serving roots configured with a freshness policy need notifications.
	if d.maxReseedAge > 0 {
		d.generationChanged = make(chan struct{})
	}
}

func (d *DRBG) servingRoot() *DRBG {
	if d.freshnessRoot != nil {
		return d.freshnessRoot
	}

	return d
}
