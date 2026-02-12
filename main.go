package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"entropy-service/rng"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	// remove below comment to enable HTTP/2
	//"golang.org/x/net/http2"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	//"crypto/rand"
	//"io"
)

type HealthInfo struct {
	Status               string `json:"status"`
	Version              string `json:"rng_version"`
	Source               string `json:"rng_source"`
	DRBG                 string `json:"rng_drbg"`
	ReseedAgeMs          int64  `json:"reseed_age_ms"`
	ReseedIntervalMs     int64  `json:"reseed_interval_ms"`
	ReseedSizeBits       int    `json:"reseed_size_bits"`
	EntropyBufferedBytes int    `json:"entropy_buffered_kb"`
	EntropyBufferedPCT   int    `json:"entropy_buffered_pct"`
}

// Buffered QRNG struct
type QRNGBuffer struct {
	buf       []byte
	mu        sync.Mutex
	capacity  int
	fillDelay time.Duration
	devPath   string
	stop      chan struct{}
}

// maps to older fetchEntropy
var qrngBuffer *QRNGBuffer

func popcount(b byte) int {
	b = b - ((b >> 1) & 0x55)
	b = (b & 0x33) + ((b >> 2) & 0x33)
	return int((b + (b >> 4)) & 0x0F)
}

func heatColor(v int) color.RGBA {
	// v in [0..8]
	t := float64(v) / 8.0

	// simple blue -> red gradient
	r := uint8(255 * t)
	g := uint8(255 * (1 - abs(t-0.5)*2))
	b := uint8(255 * (1 - t))

	return color.RGBA{r, g, b, 255}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

/*
func fetchEntropy(n int) ([]byte, error) {
    b := make([]byte, n)
    _, err := rand.Read(b) // replace with QRNG logic
    return b, err
}
*/

/*
func fetchEntropy(n int) ([]byte, error) {
    b := make([]byte, n)

    f, err := os.Open("/dev/qrandom0")
    if err != nil { return nil, err }
    defer f.Close()

    total := 0
    for total < n {
        m, err := f.Read(b[total:])
        if err != nil { return nil, err }
        total += m
    }

    return b, nil
}
*/

/*
var entropyPool []byte
var mu sync.Mutex
*/

/*
func fetchEntropy(n int) ([]byte, error) {
    mu.Lock()
    defer mu.Unlock()

    // refill pool if too small
    if len(entropyPool) < n {
        f, err := os.Open("/dev/qrandom0")
        if err != nil { return nil, err }
        tmp := make([]byte, 4096)
        for {
            m, err := f.Read(tmp)
            if err != nil {
                f.Close()
                return nil, err
            }
            entropyPool = append(entropyPool, tmp[:m]...)
            if len(entropyPool) >= n { break }
        }
        f.Close()
    }

    out := entropyPool[:n]
    entropyPool = entropyPool[n:]
    return out, nil
}
*/

func initQRNGBuffer() {
	// For example, 64 KB buffer. 2MB for testing purposes
	qrngBuffer = NewQRNGBuffer("/dev/qrandom0", 2*1024*1024)

	// Attach it to DRBG
	//drbg.SetEntropyBuffer(qrngBuffer)
}

// NewQRNGBuffer creates a buffered QRNG reader
func NewQRNGBuffer(dev string, capacity int) *QRNGBuffer {
	q := &QRNGBuffer{
		buf:       make([]byte, 0, capacity),
		capacity:  capacity,
		fillDelay: 10 * time.Millisecond,
		devPath:   dev,
		stop:      make(chan struct{}),
	}

	// Start background fill
	go q.fillLoop()
	//atomic.AddUint64(&rngBytesBuffered, uint64((q.capacity))-1)
	incBuffer()
	//incTestB(q.capacity)
	return q
}

// Stop stops the background fill goroutine
func (q *QRNGBuffer) Stop() {
	close(q.stop)
}

// Get returns n bytes from the buffer, blocking if necessary
func (q *QRNGBuffer) Get(n int) ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Wait for buffer to fill if necessary
	for len(q.buf) < n {
		q.mu.Unlock()
		time.Sleep(q.fillDelay)
		q.mu.Lock()
	}

	out := q.buf[:n]
	q.buf = q.buf[n:]
	return out, nil
}

// fillLoop continuously fills the buffer from the QRNG device
func (q *QRNGBuffer) fillLoop() {
	for {
		select {
		case <-q.stop:
			return
		default:
		}

		q.mu.Lock()
		free := q.capacity - len(q.buf)
		q.mu.Unlock()

		if free <= 0 {
			time.Sleep(q.fillDelay)
			continue
		}

		tmp := make([]byte, free)
		f, err := os.Open(q.devPath)
		if err != nil {
			time.Sleep(50 * time.Millisecond) // retry
			continue
		}

		total := 0
		for total < free {
			m, err := f.Read(tmp[total:])
			if err != nil {
				break
			}
			total += m
			incTestA(m)
		}
		f.Close()

		q.mu.Lock()
		q.buf = append(q.buf, tmp[:total]...)
		q.mu.Unlock()
	}
}

// fetchEntropy reads n bytes from the buffered QRNG
func fetchEntropy(n int) ([]byte, error) {
	if qrngBuffer == nil {
		initQRNGBuffer()
	}
	//atomic.AddUint64(&rngBufferSize, uint64(len(qrngBuffer)))
	return qrngBuffer.Get(n)
}

// reseed loop default interval: 250ms
func reseedLoop(ctx context.Context, d *rng.DRBG) {
	//ticker := time.NewTicker(10 * time.Second)
	ticker := time.NewTicker(2000 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			for range ticker.C {
				atomic.AddUint64(&rngReseeds, +1)
				entropy, err := fetchEntropy(64)
				if err != nil {
					log.Println("entropy fetch failed:", err)
					continue
				}
				if err := d.Reseed(entropy); err != nil {
					log.Println("reseed failed:", err)
				}
			}
		}
	}
}

func entropyHeatmapHandler(d *rng.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		width := 1024
		height := 1024

		img := image.NewRGBA(image.Rect(0, 0, width, height))

		buf := make([]byte, width*height)
		d.Read(buf)

		i := 0
		for y := 0; y < height; y++ {
			row := y * img.Stride
			for x := 0; x < width; x++ {
				pc := popcount(buf[i])
				c := heatColor(pc)

				off := row + x*4
				img.Pix[off+0] = c.R
				img.Pix[off+1] = c.G
				img.Pix[off+2] = c.B
				img.Pix[off+3] = 255

				i++
			}
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Refresh", "5")
		w.Header().Set("X-Entropy-Metric", "bit-popcount")
		w.Header().Set("X-RNG-Reseed-Age-ms",
			strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))
		png.Encode(w, img)
	}
}

/*
func randomImageHandler(d *rng.DRBG) http.HandlerFunc {

        buf := make([]byte, width*height*4)
        d.Read(buf)

        i := 0
        for y := 0; y < height; y++ {
            for x := 0; x < width; x++ {
                img.SetRGBA(x, y, color.RGBA{
                    R: buf[i],
                    G: buf[i+1],
                    B: buf[i+2],
                    A: 255,
                })
                i += 4
            }
        }
    }
}
*/

func randomImageHandler(d *rng.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		width := 1024
		height := 1024

		img := image.NewRGBA(image.Rect(0, 0, width, height))

		// Fill the entire backing buffer with DRBG output
		d.Read(img.Pix)

		// Force alpha channel to opaque
		for y := 0; y < height; y++ {
			row := y * img.Stride
			for x := 0; x < width; x++ {
				img.Pix[row+x*4+3] = 255
			}
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Refresh", "5")
		w.Header().Set("X-Entropy-Metric", "random-image")
		w.Header().Set("X-RNG-Reseed-Age-ms-test",
			strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))
		png.Encode(w, img)
	}
}

func randomHandler(d *rng.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := 1024
		if q := r.URL.Query().Get("bytes"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 1<<20 {
				n = v
			}
		}

		buf := make([]byte, n)
		d.Read(buf)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Entropy-Metric", "random-data")
		w.Header().Set("X-RNG-Reseed-Age-ms-test",
			strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))
		w.Write(buf)
	}
}

/*
func randomBytesHandler(d *rng.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.WriteHeaders(w)
		size := 4096
		if q := r.URL.Query().Get("bytes"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 1<<20 {
				size = v
			}
		}

		buf := make([]byte, size)
		//buf := bufPool.Get().([]byte)
		//defer bufPool.Put(buf)
		//data := d.ReadInto(buf[:size]) //n

		//io.Reader(buf)
		d.Read(buf)
		atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
		atomic.AddUint64(&httpRequests, +1)
		w.Header().Set("Content-Type", "application/octet-stream")

		//d.WriteTo(w, size)
		//w.Write(data)
		//io.Copy(w, buf)
		w.Write(buf)
	}
}
*/

func randomBytesHandler(d *rng.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// write heeaders immediately
		d.WriteHeaders(w)
		w.Header().Set("Content-Type", "application/octet-stream")
		// Derive 32 bytes from master
		seed, _ := d.Derive(32)
		//nonce, _ := d.Derive(12)

		// Create per-request DRBG
		child, _ := rng.NewDRBG(seed)
		//connDRBG := r.Context().Value("conn_drbg").(*rng.DRBG)

		size := 4096
		if q := r.URL.Query().Get("bytes"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 1<<20 {
				size = v
			}
		}
		buf := make([]byte, size)

		child.Read(buf)
		atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
		atomic.AddUint64(&httpRequests, +1)

		w.Write(buf)
	}
}

func metricsHandler(d *rng.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		metrics := d.GetMetadata()

		bytes := atomic.LoadUint64(&rngBytesGenerated) / 1024 / 1024
		reseeds := atomic.LoadUint64(&rngReseeds)
		//age := metrics.ReseedAgeMs
		age := d.ReseedAge().Milliseconds()
		bufBytes := metrics.EntropyBufferedBytes / 1024
		bufCap := metrics.EntropyFillPct
		reqs := atomic.LoadUint64(&httpRequests)
		entropy := atomic.LoadUint64(&rngBytesBuffered) // took out the division by 1024 for testing
		entropyA := atomic.LoadUint64(&rngBytesTestA)
		entropyB := atomic.LoadUint64(&rngBytesTestB)

		fmt.Fprintf(w,
			`# HELP rng_bytes_generated_total Total bytes generated by DRBG
# TYPE rng_bytes_generated_total counter
rng_mb_generated_total %d

# HELP rng_reseeds_total Total reseeds
# TYPE rng_reseeds_total counter
rng_reseeds_total %d

# HELP rng_reseed_age_ms Age since last reseed
# TYPE rng_reseed_age_ms gauge
rng_reseed_age_ms %d

# HELP qrng_buffer_bytes Current buffer fill
# TYPE qrng_buffer_bytes gauge
qrng_buffer_capacity_kb %d

# HELP qrng_buffer_capacity_bytes Buffer capacity
# TYPE qrng_buffer_capacity_bytes gauge
qrng_buffer_capacity_pct %d

# HELP http_requests_total Total HTTP requests
# TYPE http_requests_total counter
http_requests_total %d

# HELP entropy_buffer_size_kb Total size of entropy buffer
# TYPE entropy_buffer_size_kb gauge
entropy_buffer_capacity_kb %d

# HELP entropy_buffer_size_kb Total size of entropy buffer
# TYPE entropy_buffer_size_kb gauge
entropy_buffer_capacity_kb %d

# HELP entropy_buffer_size_kb Total size of entropy buffer
# TYPE entropy_buffer_size_kb gauge
entropy_buffer_capacity_kb %d
`,
			bytes,
			reseeds,
			age,
			bufBytes,
			bufCap,
			reqs,
			entropy,
			entropyA,
			entropyB,
		)
	}
}

func healthHandler(d *rng.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		meta := d.GetMetadata()

		health := HealthInfo{
			Status:               "ok",
			Version:              meta.Version,
			Source:               meta.Source,
			DRBG:                 meta.DRBG,
			ReseedAgeMs:          d.ReseedAge().Milliseconds(),
			ReseedIntervalMs:     meta.ReseedIntervalMs,
			ReseedSizeBits:       meta.ReseedSizeBits,
			EntropyBufferedBytes: meta.EntropyBufferedBytes / 1024,
			EntropyBufferedPCT:   meta.EntropyFillPct,
		}

		// keep headers
		d.WriteHeaders(w)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(health); err != nil {
			http.Error(w, "failed to encode health info", http.StatusInternalServerError)
		}
	}
}

var bufPool = sync.Pool{
	New: func() any {
		return make([]byte, 1<<20) // 1 MB
	},
}

func startHTTP(ctx context.Context, addr string, handler http.Handler, master *rng.DRBG) (*http.Server, error) {
	//ln, err := net.Listen("tcp", addr)
	//if err != nil { return nil, err }
	//tln := newTunedListener(ln)
	ln, err := newTunedListener(addr, 4<<20)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		ConnContext: func(cctx context.Context, c net.Conn) context.Context {
			//seed, _ := master.Derive(32)
			//nonce, _ := master.Derive(12)

			// derive per-connection DRBG from master
			//childDRBG, _ := rng.NewDRBG(seed)
			childDRBG, cerr := rng.NewConnectionDRBG(master) // (DRBG)
			if cerr != nil {
				return ctx
			}

			// attach to context for handlers
			return context.WithValue(cctx, "conn_drbg", childDRBG)
		},
	}

	// Serve loop
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP serve error: %v", err)
		}
	}()

	// Context-driven graceful shutdown
	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}()

	return srv, nil
}

func startHTTPS(ctx context.Context, addr string, handler http.Handler, tlsConfig *tls.Config, master *rng.DRBG) (*http.Server, error) {
	/*
		if os.Getenv("TLS") == "1" {
			tlsCfg := newTLSConfig("cert.pem", "key.pem")
			tlsCfg.Certificates = []tls.Certificate{cert}
			ln = tls.NewListener(ln, tlsCfg)
		}
	*/

	ln, err := newTunedListener(addr, 4<<20)
	if err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair("/home/max/entropy-service/cert.pem", "/home/max/entropy-service/key.pem")
	tlsConfig.Certificates = []tls.Certificate{cert}
	tlsLn := tls.NewListener(ln, tlsConfig)

	srv := &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: tlsConfig,
		ConnContext: func(cctx context.Context, c net.Conn) context.Context {
			// derive per-connection DRBG from master
			seed, _ := master.Derive(32)
			//nonce, _ := master.Derive(12)
			childDRBG, _ := rng.NewDRBG(seed)
			//childDRBG, cerr := rng.NewConnectionDRBG(master) // (DRBG)
			// attach to context for handlers
			return context.WithValue(cctx, "conn_drbg", childDRBG)
		},
	}

	// remove below comment to enable HTTP/2
	/*
		http2.ConfigureServer(srv, &http2.Server{
			MaxConcurrentStreams: 1024,
			//InitialWindowSize:    1 << 20,
			//InitialConnWindowSize: 4 << 20,
			MaxReadFrameSize:     1 << 20,
		})
	*/

	// Serve loop
	go func() {
		if err := srv.Serve(tlsLn); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTPS serve error: %v", err)
		}
	}()

	// Context-driven shutdown
	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}()

	return srv, nil
}

func main() {

	// Root context canceled on signal
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	// Initialize QRNG buffer
	qrngBuf := rng.NewQRNGBuffer("/dev/qrandom0", 2*1024*1024)

	// Initialize seed space (in bytes here)
	seed, serr := fetchEntropy(64) // 64*8 = 512 bits
	if serr != nil {
		log.Fatal(serr)
	}
	//nonce, nerr := fetchEntropy(12) // 12*8 = 96 bits
	//if nerr != nil { log.Fatal(nerr) }

	// Initialize DRBG. Note that multiple instances of DRBG are created on a per-connection basis
	drbg, derr := rng.NewDRBG(seed)
	if derr != nil {
		log.Fatal(derr)
	}

	// SetMetadata(version, source, drbg-algo, reseed-interval, reseed-size, buffer-source)
	drbg.SetMetadata("1.0.0", "QRNG-idQuantique-QuantisPCI", "ChaCha20", 2000*time.Millisecond, 256, qrngBuf)

	// Attach the QRNG buffer for dynamic header reporting
	drbg.SetEntropyBuffer(qrngBuf)

	//tln := newTunedListener(ln)

	tlsCfg := newTLSConfig("/home/max/entropy-service/cert.pem", "/home/max/entropy-service/key.pem")
	cert, err := tls.LoadX509KeyPair("/home/max/entropy-service/cert.pem", "/home/max/entropy-service/key.pem")
	if err != nil {
		log.Fatal(err)
	}
	tlsCfg.Certificates = []tls.Certificate{cert}

	masterDRBG, _ := rng.NewDRBG(seed)

	// create the multiplexed listener proto
	mux := http.NewServeMux()

	// Run permanent reseed loop
	go reseedLoop(ctx, drbg)

	mux.HandleFunc("/v1/random", randomBytesHandler(drbg)) // now reads DRBG from context
	mux.HandleFunc("/v1/test", randomHandler(drbg))
	mux.HandleFunc("/v1/image/random", randomImageHandler(drbg))
	mux.HandleFunc("/v1/image/heatmap", entropyHeatmapHandler(drbg))
	mux.HandleFunc("/health", healthHandler(drbg))
	mux.Handle("/metrics", metricsHandler(drbg))

	// start HTTP & HTTPS servers on the same mux
	httpSrv, httpErr := startHTTP(ctx, ":8080", mux, masterDRBG)
	if httpErr != nil {
		log.Fatal(httpErr)
	}

	httpsSrv, httpsErr := startHTTPS(ctx, ":8443", mux, tlsCfg, masterDRBG)
	if httpsErr != nil {
		log.Fatal(httpsErr)
	}

	log.Println("HTTP server running on :8080")
	log.Println("HTTPs server running on :8443")

	<-ctx.Done()
	log.Println("shutdown signal received")

	// Block and wait for shutdown
	//shutdownCtx, cancel := context.WithCancel(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if httpErr != nil {
		_ = httpSrv.Shutdown(shutdownCtx)
	}
	if httpsSrv != nil {
		_ = httpsSrv.Shutdown(shutdownCtx)
	}

	log.Println("shutdown complete")

}
