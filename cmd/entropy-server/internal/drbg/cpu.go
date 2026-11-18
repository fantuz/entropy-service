package drbg

import (
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/rdrand"
)

// ErrCPUBackendFailed is sticky across all connections and reseeds. Retrying a
// different source must never silently replace direct CPU output.
var ErrCPUBackendFailed = errors.New("RDRAND backend stopped after hardware failure; restart required")

// One immutable link shared by the root and all its children. RDRAND has no
// application-owned seed/key or independent per-connection software streams.
type cpuBackend struct {
	read     func([]byte) (int, error)
	failed   atomic.Bool
	notified atomic.Bool
}

func newCPUGenerator(root *DRBG) (DRBG, error) {
	var state *cpuBackend
	if root != nil {
		state = root.cpu
	} else {
		reader, err := rdrand.NewReader()
		if err != nil {
			return DRBG{}, fmt.Errorf("direct CPU backend: %w", err)
		}

		state = &cpuBackend{read: reader.Read}
	}

	trackInstance()

	return DRBG{
		cpu: state, algorithm: RDRAND, counted: true, algo: RDRAND.String(),
		Reseeded: time.Now(), failurePolicy: ReseedContinue, freshnessRoot: root,
		generationChanged: make(chan struct{}),
	}, nil
}

func (c *cpuBackend) fill(p []byte) error {
	if c.failed.Load() {
		clear(p)

		return ErrCPUBackendFailed
	}

	n, err := c.read(p)
	if err != nil || n != len(p) {
		if c.failed.CompareAndSwap(false, true) {
			log.Printf("RDRAND backend stopped: read %d/%d bytes, error=%v; restart required", n, len(p), err)
		}
	}

	// Another connection may have exhausted the hardware retry budget while
	// this one was reading. Discard its in-flight output as well.
	if c.failed.Load() {
		clear(p)

		return ErrCPUBackendFailed
	}

	return nil
}

// Publish only after Read releases every generator/policy lock. CheckGeneration
// sees the atomic latch immediately; this wakes idle/blocked WebSocket writers
// without introducing a child -> root lock-order inversion.
func (d *DRBG) publishCPUFailure() {
	if d.cpu == nil || !d.cpu.failed.Load() || !d.cpu.notified.CompareAndSwap(false, true) {
		return
	}

	root := d.servingRoot()
	root.policyMu.Lock()
	defer root.policyMu.Unlock()

	root.mu.Lock()
	defer root.mu.Unlock()

	root.notifyGenerationLocked()
}

func (d *DRBG) backendError() string {
	if d.cpu != nil && d.cpu.failed.Load() {
		return ErrCPUBackendFailed.Error()
	}

	return ""
}
