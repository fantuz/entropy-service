package drbg

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

func ageTestSeed(d *DRBG, age time.Duration) {
	d.policyMu.Lock()
	defer d.policyMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	d.Reseeded = time.Now().Add(-age)
}

func requireSeedDenied(t *testing.T, reader io.Reader) {
	t.Helper()

	buffer := bytes.Repeat([]byte{0xa5}, 64)
	if n, err := reader.Read(buffer); n != 0 || !errors.Is(err, ErrSeedUnavailable) {
		t.Fatalf("unhealthy read: n=%d error=%v", n, err)
	}

	if !bytes.Equal(buffer, bytes.Repeat([]byte{0xa5}, 64)) {
		t.Fatal("denied read modified the destination")
	}
}

func TestReseedStopProtectsExistingChildrenAndRecovers(t *testing.T) {
	for _, algorithm := range testAlgorithms() {
		t.Run(algorithm.String(), func(t *testing.T) {
			root := testGenerator(t, algorithm)
			if err := root.ConfigureReseedPolicy(ReseedStop, time.Hour); err != nil {
				t.Fatal(err)
			}

			child, err := NewConnectionDRBG(root)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Close()

			grandchild, err := NewConnectionDRBG(&child)
			if err != nil {
				t.Fatal(err)
			}
			defer grandchild.Close()

			connection := NewConnectionReader(root)
			defer connection.Close()

			if _, err := connection.Read(make([]byte, 32)); err != nil {
				t.Fatal(err)
			}

			for _, reason := range []string{"failure", "stale"} {
				if reason == "failure" {
					root.RecordReseedFailure(io.ErrUnexpectedEOF)
				} else {
					ageTestSeed(root, 2*time.Hour)
				}

				before := metrics.BytesGenerated()
				epoch, childEpoch := root.reseedEpoch.Load(), connection.epoch

				nestedConnection := NewConnectionReader(&child)
				for _, reader := range []io.Reader{root, &child, &grandchild, connection, nestedConnection} {
					requireSeedDenied(t, reader)
				}

				nestedConnection.Close()

				if _, err := NewConnectionDRBG(root); !errors.Is(err, ErrSeedUnavailable) {
					t.Fatalf("derived a child while output was denied: %v", err)
				}

				if _, err := root.Derive(64); !errors.Is(err, ErrSeedUnavailable) {
					t.Fatalf("derived seed material while output was denied: %v", err)
				}

				if metrics.BytesGenerated() != before || root.reseedEpoch.Load() != epoch || connection.epoch != childEpoch {
					t.Fatal("denied reads consumed output or altered epochs")
				}

				if err := root.Reseed(testSeed()); err != nil {
					t.Fatal(err)
				}

				for _, reader := range []io.Reader{root, &child, &grandchild, connection} {
					if n, err := reader.Read(make([]byte, 32)); err != nil || n != 32 {
						t.Fatalf("read did not recover: n=%d error=%v", n, err)
					}
				}

				if connection.epoch != root.reseedEpoch.Load() || connection.epoch == childEpoch {
					t.Fatal("persistent connection did not refresh after recovery")
				}
			}
		})
	}
}

func TestReseedContinueExplicitlyPermitsFailedAndStaleState(t *testing.T) {
	root := testGenerator(t, ChaCha20)
	if err := root.ConfigureReseedPolicy(ReseedContinue, time.Second); err != nil {
		t.Fatal(err)
	}

	root.RecordReseedFailure(io.EOF)
	ageTestSeed(root, time.Hour)

	connection := NewConnectionReader(root)
	defer connection.Close()

	for _, reader := range []io.Reader{root, connection} {
		if _, err := reader.Read(make([]byte, 64)); err != nil {
			t.Fatal(err)
		}
	}

	meta := root.GetMetadata()
	if meta.SeedState(time.Second) != "failed" || !meta.GenerationAllowed || meta.ReseedFailurePolicy != ReseedContinue {
		t.Fatalf("continue concealed degradation or denied output: %+v", meta)
	}
}

func TestReseedWatchValidationAndNotifications(t *testing.T) {
	root := testGenerator(t, ChaCha20)
	for _, tc := range []struct {
		policy ReseedFailurePolicy
		age    time.Duration
	}{{"", time.Second}, {"degrade", time.Second}, {ReseedStop, 0}, {ReseedStop, -time.Second}} {
		if err := root.ConfigureReseedPolicy(tc.policy, tc.age); err == nil {
			t.Fatalf("accepted invalid policy/age: %+v", tc)
		}
	}

	if err := root.ConfigureReseedPolicy(ReseedStop, time.Hour); err != nil {
		t.Fatal(err)
	}

	changed, deadline, err := root.WatchGeneration()
	if err != nil || deadline.IsZero() || changed == nil {
		t.Fatalf("invalid healthy watch: deadline=%v error=%v", deadline, err)
	}

	root.RecordReseedFailure(io.EOF)

	select {
	case <-changed:
	default:
		t.Fatal("failure did not notify existing watchers")
	}

	changed, _, err = root.WatchGeneration()
	if !errors.Is(err, ErrSeedUnavailable) {
		t.Fatalf("failed watch allowed output: %v", err)
	}

	if err := root.Reseed(testSeed()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-changed:
	default:
		t.Fatal("recovery did not notify watchers")
	}

	if err := root.ConfigureReseedPolicy(ReseedContinue, time.Second); err != nil {
		t.Fatal(err)
	}

	if _, deadline, err := root.WatchGeneration(); err != nil || !deadline.IsZero() {
		t.Fatalf("continue watch has an expiry: %v %v", deadline, err)
	}

	if err := root.Close(); err != nil {
		t.Fatal(err)
	}

	if err := root.CheckGeneration(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed generator is available: %v", err)
	}

	if err := root.ConfigureReseedPolicy(ReseedStop, time.Second); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed generator accepted policy: %v", err)
	}
}

func TestReseedPolicyConcurrentReadsAndTransitions(t *testing.T) {
	root := testGenerator(t, AES256CTR)
	if err := root.ConfigureReseedPolicy(ReseedStop, time.Hour); err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			connection := NewConnectionReader(root)
			defer connection.Close()

			for range 64 {
				if _, err := connection.Read(make([]byte, 4096)); err != nil && !errors.Is(err, ErrSeedUnavailable) {
					t.Errorf("unexpected concurrent read error: %v", err)
				}

				_ = root.GetMetadata()
				_, _, _ = root.WatchGeneration()
			}
		})
	}

	for range 32 {
		root.RecordReseedFailure(io.EOF)

		if err := root.Reseed(testSeed()); err != nil {
			t.Fatal(err)
		}
	}

	workers.Wait()
}
