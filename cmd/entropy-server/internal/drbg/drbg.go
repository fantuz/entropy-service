package drbg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

type Reader interface {
	Read(p []byte) (n int, err error)
}

// DRBG represents a deterministic random byte generator with observability metadata.
type DRBG struct {
	mu sync.RWMutex

	// policyMu keeps private bulk reads independent of the root cipher mutex.
	// Writes to closed, Reseeded, lastReseedError and policy fields require
	// BOTH locks, in policyMu -> mu order. Readers may hold either lock.
	// Never hold a policy read lock during source I/O or network writes.
	policyMu sync.RWMutex

	// Immutable child-to-serving-root link; all descendants use the same gate.
	freshnessRoot     *DRBG
	failurePolicy     ReseedFailurePolicy
	maxReseedAge      time.Duration
	generationChanged chan struct{}

	// Crypto state
	key             [32]byte
	nonce           [aes.BlockSize]byte
	cipher          cipher.Stream
	algorithm       Algorithm
	closed          bool
	counted         bool // True until Close releases the constructor's instance count.
	generated       uint64
	Reseeded        time.Time
	reseeds         uint64
	reseedFailures  uint64
	lastReseedError string
	reseedEpoch     atomic.Uint64

	// Observability / header metadata
	version        string
	source         string
	algo           string
	reseedInterval time.Duration
	reseedSizeBits int

	// counter for number of DRBG instances
	DRBGInstanceCnt int64
}

// Metadata contains all info needed for headers / JSON.
type Metadata struct {
	Version              string
	Source               string
	DRBG                 string
	ReseedAge            time.Duration
	ReseedIntervalMs     int64
	ReseedSizeBits       int
	EntropyBufferedBytes int
	EntropyFillPct       int
	DRBGInstanceCnt      int64
	LastReseedAt         time.Time
	ReseedsTotal         uint64
	ReseedFailuresTotal  uint64
	LastReseedError      string
	ReseedFailurePolicy  ReseedFailurePolicy
	GenerationAllowed    bool
}

// HealthInfo contains all info needed to generate JSON.
type HealthInfo struct {
	Status               string `json:"status"`
	Version              string `json:"rng_version"`
	Source               string `json:"rng_source"`
	DRBG                 string `json:"drbg"`
	ReseedAgeMs          int64  `json:"reseed_age_ms"`
	ReseedIntervalMs     int64  `json:"reseed_interval_ms"`
	ReseedSizeBits       int    `json:"reseed_size_bits"`
	EntropyBufferedBytes int    `json:"entropy_buffered_kb"`
	EntropyFillPct       int    `json:"entropy_buffered_pct"`
}

var (
	activeDRBG atomic.Int64
	peakDRBG   atomic.Int64
)

// ChaCha20 has 2^32 blocks of 64 bytes per key. Use the same operational
// ceiling for all backends; this is not a Barzakh security bound.
// Reseeding resets the byte budget.
const maxBytesPerKey uint64 = 1 << 38

// Derive is intended for bounded seed material, not bulk output.
const maxDeriveBytes = 1 << 20

func ActiveInstances() int64 {
	return activeDRBG.Load()
}

// PeakInstances is the highest concurrent number of DRBG instances created
// during this process lifetime, including the root generator.
func PeakInstances() int64 {
	return peakDRBG.Load()
}

// NewDRBG creates a new DRBG instance from a seed.
func NewDRBG(seed []byte) (DRBG, error) {
	return NewDRBGWithAlgorithm(seed, ChaCha20)
}

// NewDRBGWithAlgorithm preserves the existing SHA-512 seed construction.
// AES-CTR here is a stream backend, not a NIST CTR_DRBG implementation.
func NewDRBGWithAlgorithm(seed []byte, algorithm Algorithm) (DRBG, error) {
	return newDRBGWithRoot(seed, algorithm, nil)
}

func newDRBGWithRoot(seed []byte, algorithm Algorithm, root *DRBG) (DRBG, error) {
	if _, err := ParseAlgorithm(string(algorithm)); err != nil {
		return DRBG{}, err
	}

	if len(seed) < 32 {
		return DRBG{}, errors.New("seed must contain at least 32 bytes")
	}

	h := sha512.Sum512(seed)
	defer clear(h[:])

	var (
		key   [32]byte
		nonce [aes.BlockSize]byte
	)
	defer clear(key[:])
	defer clear(nonce[:])

	copy(key[:], h[:32])
	copy(nonce[:], h[32:48])

	c, err := newCipherStream(algorithm, &key, &nonce)
	if err != nil {
		return DRBG{}, err
	}

	active := activeDRBG.Add(1)

	for {
		peak := peakDRBG.Load()
		if active <= peak || peakDRBG.CompareAndSwap(peak, active) {
			break
		}
	}

	return DRBG{
		key:           key,
		nonce:         nonce,
		cipher:        c,
		algorithm:     algorithm,
		counted:       true,
		algo:          algorithm.String(),
		Reseeded:      time.Now(),
		failurePolicy: ReseedContinue,
		freshnessRoot: root,
	}, nil
}

// NewConnectionDRBG derives an independent child with its parent's backend.
func NewConnectionDRBG(d *DRBG) (DRBG, error) {
	if d == nil {
		return DRBG{}, errors.New("nil parent generator")
	}

	// Keep child seed material in an owned fixed-size buffer and discard it
	// immediately after constructing the independent child.
	var seed [64]byte
	defer clear(seed[:])

	if _, err := d.Read(seed[:]); err != nil {
		return DRBG{}, err
	}

	return newDRBGWithRoot(seed[:], d.algorithm, d.servingRoot())
}

// Algorithm is immutable after construction, including across reseeds.
func (d *DRBG) Algorithm() Algorithm {
	return d.algorithm
}

// Reseed mixes the current key with fresh seed bytes without assembling a
// combined heap buffer. The resulting bytes and old visible state are cleared
// after installing the new cipher; the caller still owns seed.
func (d *DRBG) Reseed(seed []byte) error {
	d.policyMu.Lock()
	defer d.policyMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return io.ErrClosedPipe
	}

	// A zero/short input cannot establish a meaningful fresh-seed event.
	// This is a length precondition, not an assertion of min-entropy.
	if len(seed) < 32 {
		return errors.New("reseed requires at least 32 input bytes")
	}

	// Streaming SHA-512 has the same digest as Sum512(append(oldKey, seed...)),
	// without allocating an old-key-plus-seed concatenation.
	h := sha512.New()
	_, _ = h.Write(d.key[:])
	_, _ = h.Write(seed)

	var digest [sha512.Size]byte
	defer clear(digest[:])

	h.Sum(digest[:0])

	var (
		key   [32]byte
		nonce [aes.BlockSize]byte
	)
	defer clear(key[:])
	defer clear(nonce[:])

	copy(key[:], digest[:32])
	copy(nonce[:], digest[32:48])

	c, err := newCipherStream(d.algorithm, &key, &nonce)
	if err != nil {
		return err
	}

	// Do not erase or replace usable state until the next cipher is ready.
	clear(d.key[:])
	clear(d.nonce[:])
	clearStreamState(d.cipher)
	d.key = key
	d.nonce = nonce
	d.cipher = c
	d.generated = 0
	d.Reseeded = time.Now()
	d.reseeds++
	d.lastReseedError = ""
	d.reseedEpoch.Add(1)
	d.notifyGenerationLocked()

	return nil
}

// Close clears the DRBG key/nonce fields we own, releases the cipher
// reference and makes further reads or reseeds fail. It is idempotent and
// serialized against Read/Reseed. Close releases the active-instance count
// exactly once; callers must not manually decrement counters. Do not copy a
// DRBG after its first use. Opaque cipher implementation state and copies
// made by the Go runtime cannot be guaranteed erased.
func (d *DRBG) Close() error {
	d.policyMu.Lock()
	defer d.policyMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}

	clear(d.key[:])
	clear(d.nonce[:])
	clearStreamState(d.cipher)
	d.cipher = nil
	d.closed = true
	d.notifyGenerationLocked()

	if d.counted {
		d.counted = false

		activeDRBG.Add(-1)
	}

	return nil
}

// Read fills p with pseudo-random bytes.
func (d *DRBG) Read(p []byte) (int, error) {
	if root := d.freshnessRoot; root != nil {
		root.policyMu.RLock()
		defer root.policyMu.RUnlock()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if root := d.freshnessRoot; root != nil {
		if err := root.generationErrorLocked(time.Now()); err != nil {
			return 0, err
		}
	}

	return d.readLocked(p)
}

// readLocked requires d.mu; seed derivation also snapshots the reseed epoch.
func (d *DRBG) readLocked(p []byte) (int, error) {
	if err := d.generationErrorLocked(time.Now()); err != nil {
		return 0, err
	}

	if uint64(len(p)) > maxBytesPerKey-d.generated {
		return 0, errors.New("DRBG output limit reached: reseed required")
	}

	clear(p)
	d.cipher.XORKeyStream(p, p)
	d.generated += uint64(len(p))
	metrics.AddBytesGenerated(len(p))

	return len(p), nil
}

// ReseedAge returns how long since last reseed.
func (d *DRBG) ReseedAge() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return time.Since(d.Reseeded)
}

// RecordReseedFailure marks a failed or skipped refresh without changing the
// key, byte budget, reseed timestamp, or epoch. A successful Reseed clears it.
func (d *DRBG) RecordReseedFailure(err error) {
	if err == nil {
		return
	}

	d.policyMu.Lock()
	defer d.policyMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	d.reseedFailures++
	d.lastReseedError = err.Error()
	d.notifyGenerationLocked()
}

// WriteHeaders reports a synchronized metadata snapshot. The old software
// QRNG buffer has been removed; its compatibility headers remain zero.
func (d *DRBG) WriteHeaders(w http.ResponseWriter) {
	meta := d.GetMetadata()
	w.Header().Set("X-RNG-Version", meta.Version)
	w.Header().Set("X-RNG-Source", meta.Source)
	w.Header().Set("X-RNG-DRBG", meta.DRBG)
	w.Header().Set("X-RNG-Reseed-Age-ms", strconv.FormatInt(meta.ReseedAge.Milliseconds(), 10))
	w.Header().Set("X-RNG-Reseed-Interval-ms", strconv.FormatInt(meta.ReseedIntervalMs, 10))
	w.Header().Set("X-RNG-Reseed-Size-bits", strconv.Itoa(meta.ReseedSizeBits))
	w.Header().Set("X-RNG-Entropy-Buffered-kB", "0")
	w.Header().Set("X-RNG-Entropy-Buffered-%", "0")
}

// SetMetadata sets the root generator's metadata. sizeBits is measured in bits.
func (d *DRBG) SetMetadata(version, source, _ string, interval time.Duration, sizeBits int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.version = version
	d.source = source
	d.algo = d.algorithm.String()
	d.reseedInterval = interval
	d.reseedSizeBits = sizeBits
}

// GetMetadata returns one synchronized snapshot; removed buffer fields stay zero.
func (d *DRBG) GetMetadata() Metadata {
	d.mu.RLock()
	defer d.mu.RUnlock()

	now := time.Now()

	return Metadata{
		Version:             d.version,
		Source:              d.source,
		DRBG:                d.algo,
		ReseedAge:           now.Sub(d.Reseeded),
		LastReseedAt:        d.Reseeded.UTC(),
		ReseedIntervalMs:    d.reseedInterval.Milliseconds(),
		ReseedSizeBits:      d.reseedSizeBits,
		DRBGInstanceCnt:     ActiveInstances(),
		ReseedsTotal:        d.reseeds,
		ReseedFailuresTotal: d.reseedFailures,
		LastReseedError:     d.lastReseedError,
		ReseedFailurePolicy: d.failurePolicy,
		GenerationAllowed:   d.generationErrorLocked(now) == nil,
	}
}

// Derive draws bounded seed material from the synchronized DRBG Read path.
// Requests outside 1..maxDeriveBytes are rejected before allocating or reading.
func (d *DRBG) Derive(seedSize int) ([]byte, error) {
	if seedSize < 1 || seedSize > maxDeriveBytes {
		return nil, fmt.Errorf("derive size must be between 1 and %d bytes", maxDeriveBytes)
	}

	seed := make([]byte, seedSize)
	if _, err := d.Read(seed); err != nil {
		clear(seed)

		return nil, err
	}

	return seed, nil
}
