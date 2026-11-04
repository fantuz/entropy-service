package drbg

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

func TestReseedFailurePreservesStreamAndRecovery(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d, reference := testGenerator(t, algorithm), testGenerator(t, algorithm)
			before := d.GetMetadata()
			epoch := d.reseedEpoch.Load()
			d.RecordReseedFailure(nil)
			d.RecordReseedFailure(io.ErrUnexpectedEOF)

			failed := d.GetMetadata()
			if failed.ReseedFailuresTotal != 1 || failed.LastReseedError != io.ErrUnexpectedEOF.Error() ||
				!failed.LastReseedAt.Equal(before.LastReseedAt) || d.reseedEpoch.Load() != epoch {
				t.Fatal("failure changed seed state or did not record the error")
			}

			got, want := make([]byte, 64), make([]byte, 64)
			if _, err := io.ReadFull(d, got); err != nil {
				t.Fatal(err)
			}

			if _, err := io.ReadFull(reference, want); err != nil || !bytes.Equal(got, want) {
				t.Fatal("recording a failure changed the generated stream")
			}

			if err := d.Reseed(testSeed()); err != nil {
				t.Fatal(err)
			}

			recovered := d.GetMetadata()
			if recovered.LastReseedError != "" || recovered.ReseedFailuresTotal != 1 || recovered.ReseedsTotal != 1 {
				t.Fatal("recovery must clear the current error and preserve cumulative failure count")
			}
		})
	}
}

func TestGeneratedBytesCountSuccessfulReadsOnly(t *testing.T) {
	d := testGenerator(t, ChaCha20)
	before := metrics.BytesGenerated()

	if _, err := d.Read(make([]byte, 17)); err != nil {
		t.Fatal(err)
	}

	child, err := NewConnectionDRBG(d)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := child.Read(make([]byte, 23)); err != nil {
		t.Fatal(err)
	}

	d.generated = maxBytesPerKey
	if _, err := d.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected exhausted generator to fail")
	}

	if got := metrics.BytesGenerated() - before; got != 17+64+23 {
		t.Fatalf("successful data reads and child seed derivation: got %d bytes", got)
	}
}

func TestConcurrentMetadataAndReseedFailureSnapshots(t *testing.T) {
	d := testGenerator(t, AES256CTR)

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range 32 {
				d.SetMetadata("test", "test", "ignored", time.Second, 2048)
				_ = d.GetMetadata()
				d.WriteHeaders(httptest.NewRecorder())
			}
		}()
	}

	for range 32 {
		d.RecordReseedFailure(errors.New("source unavailable"))

		if err := d.Reseed(testSeed()); err != nil {
			t.Error(err)
		}
	}

	wg.Wait()
}

func TestReseedRejectsUndersizedInputWithoutFreshnessClaim(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, algorithm)
			reference := testGenerator(t, algorithm)
			before := d.GetMetadata()
			epoch := d.reseedEpoch.Load()

			for _, size := range []int{0, 1, 31} {
				if err := d.Reseed(make([]byte, size)); err == nil {
					t.Fatalf("accepted %d-byte reseed as fresh", size)
				}
			}

			after := d.GetMetadata()
			if after.ReseedsTotal != before.ReseedsTotal ||
				!after.LastReseedAt.Equal(before.LastReseedAt) ||
				d.reseedEpoch.Load() != epoch || d.generated != reference.generated {
				t.Fatal("invalid reseeds changed state or claimed successful freshness")
			}

			got, want := make([]byte, 128), make([]byte, 128)
			if _, err := io.ReadFull(d, got); err != nil {
				t.Fatal(err)
			}

			if _, err := io.ReadFull(reference, want); err != nil || !bytes.Equal(got, want) {
				t.Fatal("invalid reseed modified the deterministic output stream")
			}

			if err := d.Reseed(bytes.Repeat([]byte{0x51}, 32)); err != nil {
				t.Fatalf("minimum supported reseed was rejected: %v", err)
			}
		})
	}
}
