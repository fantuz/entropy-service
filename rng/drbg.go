package rng

import (
	"golang.org/x/crypto/chacha20"
	"crypto/cipher"
	"crypto/sha512"
	"crypto/sha256"
	"strconv"
	"sync"
	"time"
	"net/http"
	//"io"
)

// DRBG represents a deterministic random byte generator with observability metadata
type DRBG struct {
	mu sync.Mutex
	stream cipher.Stream
	// Crypto state
	key      [32]byte
	nonce    [12]byte
	cipher   *chacha20.Cipher
	reseeded time.Time

	// Observability / header metadata
	version        string
	source         string
	algo           string
	reseedInterval time.Duration
	//reseedInterval int64
	reseedSizeBits int

	// optional: pointer to external entropy buffer
	entropyBuf *QRNGBuffer
}

// Metadata contains all info needed for headers / JSON
type Metadata struct {
	Version			string
	Source			string
	DRBG			string
	ReseedAgeMs		time.Duration
	ReseedIntervalMs	int64
	ReseedSizeBits		int
	EntropyBufferedBytes	int
	EntropyFillPct		int
}

// HealthInfo contains all info needed to generate JSON
type HealthInfo struct {
	Status			string `json:"status"`
	Version			string `json:"rng_version"`
	Source			string `json:"rng_source"`
	DRBG			string `json:"drbg"`
	ReseedAgeMs		int64  `json:"reseed_age_ms"`
	ReseedIntervalMs	int64  `json:"reseed_interval_ms"`
	ReseedSizeBits		int    `json:"reseed_size_bits"`
	EntropyBufferedBytes	int    `json:"entropy_buffered_kb"`
	EntropyFillPct		int    `json:"entropy_buffered_pct"`
}

func (d *DRBG) SetEntropyBuffer(q *QRNGBuffer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entropyBuf = q
}

// NewDRBG creates a new DRBG instance from a seed
func NewDRBG(seed []byte, noncee []byte) (*DRBG, error) {
	/*
	if len(seed) < 32 {
		panic("seed too short")
	}
	*/
	h := sha512.Sum512(seed)
	n := sha256.Sum256(noncee)
	//n := sha256.Sum256(noncee)

	//if _, err := crypto/rand.Read(nonce); err != nil
	if len(seed) < 12 {
		panic("failed to generate nonce")
	}

	var key [32]byte
	var nonce [12]byte
	//nonce := make([]byte, 12)
	copy(key[:], h[:32])
	//copy(nonce[:], h[32:44])
	copy(noncee[:], n[:32])

	c, err := chacha20.NewUnauthenticatedCipher(key[:], noncee[:])
	if err != nil {
		return nil, err
	}

	return &DRBG{
		key:      key,
		nonce:    nonce,
		cipher:   c,
		reseeded: time.Now(),
	}, nil
}

// pooled DRBG, per-connection
func NewConnectionDRBG(d *DRBG) (*DRBG, error) {
	seed, err := d.Derive(32) // 256-bit seed
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, 12)
	copy(nonce, seed[:12])

	return NewDRBG(seed, nonce)
}

// Reseed mixes new entropy into the DRBG
func (d *DRBG) Reseed(seed []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	h := sha512.Sum512(append(d.key[:], seed...))
	copy(d.key[:], h[:32])
	copy(d.nonce[:], h[32:44])

	c, err := chacha20.NewUnauthenticatedCipher(d.key[:], d.nonce[:])
	if err != nil {
		return err
	}
	d.cipher = c
	d.reseeded = time.Now()
	return nil
}

// Read fills p with pseudo-random bytes
func (d *DRBG) Read(p []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cipher.XORKeyStream(p, p)
}

// ReseedAge returns how long since last reseed
func (d *DRBG) ReseedAge() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.reseeded)
}

// WriteHeaders writes all observability headers to an http.ResponseWriter
func (d *DRBG) WriteHeaders(w http.ResponseWriter) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	w.Header().Set("X-RNG-Version", d.version)
	w.Header().Set("X-RNG-Source", d.source)
	w.Header().Set("X-RNG-DRBG", d.algo)

	w.Header().Set(
		"X-RNG-Reseed-Age-ms",
		strconv.FormatInt(now.Sub(d.reseeded).Milliseconds(), 10),
	)

	w.Header().Set(
		"X-RNG-Reseed-Interval-ms",
		strconv.FormatInt(d.reseedInterval.Milliseconds(), 10),
	)

	w.Header().Set(
		"X-RNG-Reseed-Size-bits",
		strconv.Itoa(d.reseedSizeBits),
	)

	bufKB := 0
	bufPct := 0

	if d.entropyBuf != nil {
		d.entropyBuf.mu.Lock()
		bufKB = len(d.entropyBuf.buf) / 1024
		bufPct = (len(d.entropyBuf.buf) * 100) / d.entropyBuf.capacity
		d.entropyBuf.mu.Unlock()
	}
	w.Header().Set("X-RNG-Entropy-Buffered-kB", strconv.Itoa(bufKB))
	w.Header().Set("X-RNG-Entropy-Buffered-%", strconv.Itoa(bufPct))
}

// SetMetadata sets all DRBG metadata
func (d *DRBG) SetMetadata(version, source, algo string, interval time.Duration, sizeBits int, buf *QRNGBuffer) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.version = version
	d.source = source
	d.algo = algo
	d.reseedInterval = interval
	d.reseedSizeBits = sizeBits
	d.entropyBuf = buf
}

// GetMetadata returns a snapshot of metadata
//func (d *DRBG) GetMetadata() (version, source, algo string, reseedInterval, reseedSizeBits, bufKB, bufPct) 
func (d *DRBG) GetMetadata() Metadata {
    bufKB := 0
    bufPct := 0

    if d.entropyBuf != nil {
        //d.mu.Lock()
        d.entropyBuf.mu.Lock()
        //defer d.mu.Unlock()
        bufKB = len(d.entropyBuf.buf)
        bufPct = len(d.entropyBuf.buf) * 100 / d.entropyBuf.capacity
        d.entropyBuf.mu.Unlock()
    }

    return Metadata{
    	Version:		d.version,
    	Source:			d.source,
    	DRBG:			d.algo,
	//ReseedAgeMs:		now.Sub(d.reseeded).Milliseconds(),
    	ReseedIntervalMs:	d.reseedInterval.Milliseconds(),
    	ReseedSizeBits:		d.reseedSizeBits,
    	EntropyBufferedBytes:	bufKB,
    	EntropyFillPct:		bufPct,
    }
    
}

func (d *DRBG) ReadInto(dst []byte) {
    d.mu.Lock()
    defer d.mu.Unlock()
    d.stream.XORKeyStream(dst, dst)
    // update counters, bytes generated, reseed checks, etc
}

func (d *DRBG) Write(p []byte) (int, error) {
    d.ReadInto(p)
    return len(p), nil
}

// for later fix, 
/*
func (d *DRBG) Read(p []byte) (int, error) {
    // fill p
    return len(p), nil
}
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
	d.Read(seed)
	return seed, nil
}

/*
func (d *DRBG) WriteTo(w io.Writer, n int) error {
    //buf := d.bufPool.Get().([]byte)
    buf := d.entropyBuf.Get()
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

