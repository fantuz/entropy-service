package drbg

import (
	"crypto/cipher"
	"crypto/sha512"
	"golang.org/x/crypto/chacha20"
	//"crypto/sha256"
	"net/http"
	"strconv"
	//"sync"
	"sync/atomic"
	"time"
	//"io"
	"github.com/fantuz/entropy-service/entropy-server/internal/qrng"
)

type Reader interface {
	Read(p []byte) (n int, err error)
}

// DRBG represents a deterministic random byte generator with observability metadata
type DRBG struct {
	// LOCKS
	//Mu     sync.Mutex
	Stream cipher.Stream

	// Crypto state
	key      [32]byte
	nonce    [12]byte
	cipher   *chacha20.Cipher
	Reseeded time.Time

	// Observability / header metadata
	version        string
	source         string
	algo           string
	reseedInterval time.Duration
	//reseedInterval int64
	reseedSizeBits int

	// optional: pointer to external entropy buffer
	entropyBuf qrng.QRNGBuffer

	// counter for number of DRBG instances
	DRBGInstanceCnt int64
}

// Metadata contains all info needed for headers / JSON
type Metadata struct {
	Version              string
	Source               string
	DRBG                 string
	ReseedAgeMs          time.Duration
	ReseedIntervalMs     int64
	ReseedSizeBits       int
	EntropyBufferedBytes int
	EntropyFillPct       int
	DRBGInstanceCnt      int64
}

// HealthInfo contains all info needed to generate JSON
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

func DecreaseActiveInstances(quantity int64) {
	activeDRBG.Add(-1)
}

func ActiveInstances() int64 {
	return activeDRBG.Load()
}

func (d DRBG) SetEntropyBuffer(q qrng.QRNGBuffer) {
	//d.Mu.Lock()
	//defer d.Mu.Unlock()
	//d.Mu = sync.Mutex{}
	d.entropyBuf = q
}

// NewDRBG creates a new DRBG instance from a seed
func NewDRBG(seed []byte) (DRBG, error) {
	if len(seed) < 32 {
		panic("seed too short")
	}
	h := sha512.Sum512(seed)
	//n := sha256.Sum256(noncee)

	//if _, err := crypto/rand.Read(nonce); err != nil

	var key [32]byte
	var nonce [12]byte
	//nonce := make([]byte, 12)
	copy(key[:], h[:32])
	copy(nonce[:], h[32:44])
	//copy(nonce[:], n[:12])

	c, _ := chacha20.NewUnauthenticatedCipher(key[:], nonce[:])

	/*
		if err != nil {
			return _, err
		}
	*/

	activeDRBG.Add(1)
	//return &DRBG
	return DRBG{
		key:      key,
		nonce:    nonce,
		cipher:   c,
		Reseeded: time.Now(),
	}, nil
}

// DRBG per-connection seed
func NewConnectionDRBG(d DRBG) (DRBG, error) {
	//seed, err := d.Derive(32) // 256-bit seed
	seed, _ := d.Derive(32) // 256-bit seed

	/*
		if err != nil {
			return nil, err
		}
	*/

	//nonce := make([]byte, 12) // 96-bit nonce
	//copy(nonce, seed[:12])

	return NewDRBG(seed)
}

// Reseed mixes new entropy into the DRBG
//func (d DRBG) Reseed(seed []byte) error
func (d *DRBG) Reseed(seed []byte) error {
	//d.Mu.Lock()
	//defer d.Mu.Unlock()

	h := sha512.Sum512(append(d.key[:], seed...))
	copy(d.key[:], h[:32])
	copy(d.nonce[:], h[32:44])

	c, err := chacha20.NewUnauthenticatedCipher(d.key[:], d.nonce[:])
	if err != nil {
		return err
	}
	d.cipher = c
	d.Reseeded = time.Now()
	//activeDRBG.Add(-1)
	return nil
}

// Read fills p with pseudo-random bytes
func (d *DRBG) Read(p []byte) (int, error) {
	//d.Mu.Lock()
	//defer d.Mu.Unlock()
	d.cipher.XORKeyStream(p, p)
	//d.cipher.XORKeyStream(p.CH, p.CH)
	//d.CH <- generateEntropyChunk()

	//d.Mu.Lock()
	//defer d.Mu.Unlock()

	// Fill p with pseudorandom data
	//err := d.cipher.XORKeyStream(p, p)
	//err := d.generate(p)
	/*
		if err != nil {
		    return 0, err
		}
	*/

	//return d.cipher.XORKeyStream(p, p), nil
	return len(p), nil
	//return int(p), nil
}

// ReseedAge returns how long since last reseed
func (d *DRBG) ReseedAge() time.Duration {
	//d.Mu.Lock()
	//defer d.Mu.Unlock()
	return time.Since(d.Reseeded)
}

// WriteHeaders writes all observability headers to an http.ResponseWriter
func (d *DRBG) WriteHeaders(w http.ResponseWriter) {
	//d.Mu.Lock()
	//defer d.Mu.Unlock()

	now := time.Now()

	w.Header().Set("X-RNG-Version", d.version)
	w.Header().Set("X-RNG-Source", d.source)
	w.Header().Set("X-RNG-DRBG", d.algo)

	w.Header().Set(
		"X-RNG-Reseed-Age-ms",
		strconv.FormatInt(now.Sub(d.Reseeded).Milliseconds(), 10),
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

	if d.entropyBuf.CH != nil {
		//d.entropyBuf.Mu.Lock()
		//*qrng.QRNGBuffer.Mu.Lock()
		//bufKB = len(d.entropyBuf.Buf) / 1024
		//bufPct = (len(d.entropyBuf.Buf) * 100) / d.entropyBuf.Capacity
		bufKB = len(d.entropyBuf.CH) / 1024
		bufPct = (len(d.entropyBuf.CH) * 100) / d.entropyBuf.Capacity
		//d.entropyBuf.Mu.Unlock()
	}

	w.Header().Set("X-RNG-Entropy-Buffered-kB", strconv.Itoa(bufKB))
	w.Header().Set("X-RNG-Entropy-Buffered-%", strconv.Itoa(bufPct))
}

// SetMetadata sets all DRBG metadata
func (d *DRBG) SetMetadata(version, source, algo string, interval time.Duration, sizeBits int, buf qrng.QRNGBuffer) {
	//d.Mu.Lock()
	//defer d.Mu.Unlock()

	d.version = version
	d.source = source
	d.algo = algo
	d.reseedInterval = interval
	d.reseedSizeBits = sizeBits
	d.entropyBuf = buf
	//d.DRBGInstanceNumber = activeDRBG
}

// GetMetadata returns a snapshot of metadata
func (d *DRBG) GetMetadata() Metadata {
	bufKB := 0
	bufPct := 0

	//if d.entropyBuf != nil
	if d.entropyBuf.CH != nil {
		//d.Mu.Lock() // old
		//d.entropyBuf.Mu.Lock()
		//defer d.Mu.Unlock()
		//bufKB = len(d.entropyBuf.CH)
		//bufPct = len(d.entropyBuf.CH) * 100 / d.entropyBuf.Capacity
		bufKB = len(d.entropyBuf.Buf)
		bufPct = len(d.entropyBuf.Buf) * 100 / d.entropyBuf.Capacity
		//d.entropyBuf.Mu.Unlock()
	}

	return Metadata{
		Version: d.version,
		Source:  d.source,
		DRBG:    d.algo,
		//ReseedAgeMs:		now.Sub(d.reseeded).Milliseconds(),
		ReseedIntervalMs:     d.reseedInterval.Milliseconds(),
		ReseedSizeBits:       d.reseedSizeBits,
		EntropyBufferedBytes: bufKB,
		EntropyFillPct:       bufPct,
		//DRBGInstanceCnt:        d.DRBGInstanceCnt,
		DRBGInstanceCnt: ActiveInstances(),
	}

}

func (d *DRBG) ReadInto(dst []byte) {
	//d.Mu.Lock()
	//defer d.Mu.Unlock()
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
	//d.Read(seed)
	_, err := d.Read(seed)
	if err != nil {
		return nil, err
	}
	return seed, nil
}

/*
func (d *DRBG) WriteTo(w io.Writer, n int) error {
    buf := d.entropyBuf.Get(n).([]byte)
    //buf := d.bufPool.Get().([]byte)
    //buf := d.entropyBuf.Get()
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
