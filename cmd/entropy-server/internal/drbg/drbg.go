package drbg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"errors"
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
	// LOCKS
	// Mu     sync.Mutex
	mu     sync.Mutex
	Stream cipher.Stream

	// Crypto state
	key             [32]byte
	nonce           [aes.BlockSize]byte
	cipher          cipher.Stream
	algorithm       Algorithm
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
	// reseedInterval int64
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

var activeDRBG atomic.Int64

// ChaCha20 has 2^32 blocks of 64 bytes per key. Use the same operational
// ceiling for both backends; reseeding resets the byte budget.
const maxBytesPerKey uint64 = 1 << 38

func DecreaseActiveInstances(quantity int64) {
	activeDRBG.Add(-quantity)
}

func ActiveInstances() int64 {
	return activeDRBG.Load()
}

// NewDRBG creates a new DRBG instance from a seed.
func NewDRBG(seed []byte) (DRBG, error) {
	return NewDRBGWithAlgorithm(seed, ChaCha20)
}

// NewDRBGWithAlgorithm preserves the existing SHA-512 seed construction.
// AES-CTR here is a stream backend, not a NIST CTR_DRBG implementation.
func NewDRBGWithAlgorithm(seed []byte, algorithm Algorithm) (DRBG, error) {
	if _, err := ParseAlgorithm(string(algorithm)); err != nil {
		return DRBG{}, err
	}

	if len(seed) < 32 {
		return DRBG{}, errors.New("seed must contain at least 32 bytes")
	}

	h := sha512.Sum512(seed)

	var (
		key   [32]byte
		nonce [aes.BlockSize]byte
	)
	copy(key[:], h[:32])
	copy(nonce[:], h[32:48])

	c, err := newCipherStream(algorithm, key, nonce)
	if err != nil {
		return DRBG{}, err
	}

	activeDRBG.Add(1)

	return DRBG{
		key:       key,
		nonce:     nonce,
		cipher:    c,
		algorithm: algorithm,
		algo:      algorithm.String(),
		Reseeded:  time.Now(),
	}, nil
}

// NewConnectionDRBG derives an independent child with its parent's backend.
func NewConnectionDRBG(d *DRBG) (DRBG, error) {
	seed, err := d.Derive(64)
	if err != nil {
		return DRBG{}, err
	}

	return NewDRBGWithAlgorithm(seed, d.algorithm)
}

// Algorithm is immutable after construction, including across reseeds.
func (d *DRBG) Algorithm() Algorithm {
	return d.algorithm
}

// Reseed mixes new entropy into the DRBG
func (d *DRBG) Reseed(seed []byte) error {
	// d.Mu.Lock()
	// defer d.Mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()

	h := sha512.Sum512(append(d.key[:], seed...))

	var (
		key   [32]byte
		nonce [aes.BlockSize]byte
	)

	copy(key[:], h[:32])
	copy(nonce[:], h[32:48])

	c, err := newCipherStream(d.algorithm, key, nonce)
	if err != nil {
		return err
	}

	d.key = key
	d.nonce = nonce
	d.cipher = c
	d.generated = 0
	d.Reseeded = time.Now()
	d.reseeds++
	d.lastReseedError = ""
	d.reseedEpoch.Add(1)
	// activeDRBG.Add(-1)
	return nil
}

// Read fills p with pseudo-random bytes.
func (d *DRBG) Read(p []byte) (int, error) {
	// d.Mu.Lock()
	// defer d.Mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.readLocked(p)
}

// readLocked requires d.mu; seed derivation also snapshots the reseed epoch.
func (d *DRBG) readLocked(p []byte) (int, error) {
	if uint64(len(p)) > maxBytesPerKey-d.generated {
		return 0, errors.New("DRBG output limit reached: reseed required")
	}

	clear(p)
	d.cipher.XORKeyStream(p, p)
	d.generated += uint64(len(p))
	metrics.AddBytesGenerated(len(p))
	// d.cipher.XORKeyStream(p.CH, p.CH)
	// d.CH <- generateEntropyChunk()

	// d.Mu.Lock()
	// defer d.Mu.Unlock()

	// Fill p with pseudorandom data
	// err := d.cipher.XORKeyStream(p, p)
	// err := d.generate(p)
	/*
		if err != nil {
		    return 0, err
		}
	*/

	// return d.cipher.XORKeyStream(p, p), nil
	return len(p), nil
	// return int(p), nil
}

// ReseedAge returns how long since last reseed.
func (d *DRBG) ReseedAge() time.Duration {
	// d.Mu.Lock()
	// defer d.Mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()

	return time.Since(d.Reseeded)
}

// RecordReseedFailure marks a failed or skipped refresh without changing the
// key, byte budget, reseed timestamp, or epoch. A successful Reseed clears it.
func (d *DRBG) RecordReseedFailure(err error) {
	if err == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.reseedFailures++
	d.lastReseedError = err.Error()
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
	d.mu.Lock()
	defer d.mu.Unlock()

	return Metadata{
		Version:             d.version,
		Source:              d.source,
		DRBG:                d.algo,
		ReseedAge:           time.Since(d.Reseeded),
		LastReseedAt:        d.Reseeded.UTC(),
		ReseedIntervalMs:    d.reseedInterval.Milliseconds(),
		ReseedSizeBits:      d.reseedSizeBits,
		DRBGInstanceCnt:     ActiveInstances(),
		ReseedsTotal:        d.reseeds,
		ReseedFailuresTotal: d.reseedFailures,
		LastReseedError:     d.lastReseedError,
	}
}

func (d *DRBG) ReadInto(dst []byte) {
	// d.Mu.Lock()
	// defer d.Mu.Unlock()
	d.Stream.XORKeyStream(dst, dst)
	// update counters, bytes generated, reseed checks, etc
}

func (d *DRBG) Write(p []byte) (int, error) {
	d.ReadInto(p)

	return len(p), nil
}

/*
func (d *DRBG) Derive(seedSize int) ([]byte, error) {
	seed := make([]byte, seedSize)
	_, err := d.Read(seed)
	if err != nil {
		return nil, err
	}
	return seed, nil
}
*/

func (d *DRBG) Derive(seedSize int) ([]byte, error) {
	seed := make([]byte, seedSize)
	// d.Read(seed)
	_, err := d.Read(seed)
	if err != nil {
		return nil, err
	}

	return seed, nil
	// return payload, nil
}

/*
func (d *DRBG) WriteTo(w io.Writer, n int) error {
    buf := d.entropyBuf.Get(n).([]byte)
    // buf := d.bufPool.Get().([]byte)
    // buf := d.entropyBuf.Get()
    defer d.bufPool.Put(buf)

    for n > 0 {
        chunk := min(n, len(buf))
        d.Fill(buf[:chunk])
        _, err := w.Write(buf[:chunk])
        if err != nil {
            return err
        }
        n -= chunk
    }
    return nil
}
*/
