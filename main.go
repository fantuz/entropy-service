package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"math/big"
	//"net/http"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	//"golang.org/x/net/http2" // remove comment to enable HTTP/2
	"entropy-service/rng"
	"github.com/8ff/diceware"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type EntropySource interface {
	Read(p []byte) (int, error)
}

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

type EntropyFrame struct {
	Words string `json:"words"`
	Hash  string `json:"hash"`
}

type EntropyDataFrame struct {
	Hex    string `json:"hex"`
	Base64 string `json:"base64"`
	Hash   string `json:"hash"`
}

const DefaultCSPRNG = "/dev/urandom"

//go:embed web/*
var webFS embed.FS

// maps to older fetchEntropy
var qrngBuffer *QRNGBuffer

var bufPool = sync.Pool{
	New: func() any {
		return make([]byte, 1<<20) // 1 MB
	},
}

/*
func ActiveInstances() int64 {
	return activeDRBG.Load()
}
*/

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

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

func NewEntropySource(path string, require bool) (EntropySource, error) {
	if path == "" {
		if require {
			return nil, fmt.Errorf("device required but not specified")
		}
		return rand.Reader, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if require {
			return nil, fmt.Errorf("cannot open required device: %w", err)
		}
		log.Printf("Falling back to crypto/rand: %v\n", err)
		return rand.Reader, nil
	}

	return f, nil
}

func initQRNGBuffer(dev string, l int) {
	// size in MB
	qrngBuffer = NewQRNGBuffer(dev, l*1024*1024)
	// report in KB
	log.Printf("initQRNG() set to %v KB", l*1024)

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
	atomic.AddUint64(&rngBytesBuffered, uint64((q.capacity)))
	//incBuffer()

	return q
}

// Stops the background fill goroutine
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
	incTestB(len(q.buf))
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
func fetchEntropy(n int, dev string, bufferlen int) ([]byte, error) {
	if qrngBuffer == nil {
		initQRNGBuffer(dev, bufferlen)
	}
	//atomic.AddUint64(&rngBufferSize, uint64(len(qrngBuffer)))
	incTestA(n)
	return qrngBuffer.Get(n)
}

// reseed loop
func reseedLoop(ctx context.Context, d *rng.DRBG, interval int, dev string, bufferlen int, reseedbuf int) {
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			for range ticker.C {
				entropy, eerr := fetchEntropy(reseedbuf, dev, bufferlen)
				if eerr != nil {
					log.Println("entropy fetch failed:", eerr)
					continue
				}
				if rerr := d.Reseed(entropy); rerr != nil {
					log.Println("reseed failed:", rerr)
				}
				atomic.AddUint64(&rngReseeds, +1)
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

/*
import (
    "fmt"
    "image"
    _ "image/jpeg"
    "io/ioutil"
    "os"
    "path/filepath"
)

const dir_to_scan string = "/home/da/to_merge"

func main() {
    files, _ := ioutil.ReadDir(dir_to_scan)
    for _, imgFile := range files {

        if reader, err := os.Open(filepath.Join(dir_to_scan, imgFile.Name())); err == nil {
            defer reader.Close()
            im, _, err := image.DecodeConfig(reader)
            if err != nil {
                fmt.Fprintf(os.Stderr, "%s: %v\n", imgFile.Name(), err)
                continue
            }
            fmt.Printf("%s %d %d\n", imgFile.Name(), im.Width, im.Height)
        } else {
            fmt.Println("Impossible to open the file:", err)
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
		atomic.AddUint64(&rngBytesGenerated, uint64(len(img.Pix)))
		atomic.AddUint64(&httpRequests, +1)
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

func join(words []string, sep string) string {
	result := ""
	for i, w := range words {
		if i > 0 {
			result += sep
		}
		result += w
	}
	return result
}

/*
func projectWords(n *big.Int, count int) []string {
    base := big.NewInt(int64(len(dicewareWords)))
    words := make([]string, 0, count)

    for i := 0; i < count; i++ {
        mod := new(big.Int)
        n.DivMod(n, base, mod)
        words = append(words, dicewareWords[mod.Int64()])
    }

    return words
}
*/

func wsWordsHandler(d *rng.DRBG, quantity int, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		atomic.AddUint64(&httpRequests, +1)
		conn, cerr := upgrader.Upgrade(w, r, nil)
		if cerr != nil {
			log.Println("ws upgrade failed")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer conn.Close()
		defer ticker.Stop()

		if q := r.URL.Query().Get("words"); q != "" {
			if v, verr := strconv.Atoi(q); verr == nil && v > 0 && v <= 7776 {
				quantity = v
			}
		}

		if x := r.URL.Query().Get("refresh"); x != "" {
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 1<<20 {
				dtime := time.Duration(z)
				refresh = dtime
			}
		}

		// read cycle, to detect ghost clients and ensure proper close
		go func() {
			defer cancel()

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					//ctx.Done() <- 0
					break
				}
			}
			//quit <- 0
		}()

		for {

			randomWords := diceware.GetRandomWords()
			//base := big.NewInt(int64(len(randomWords)))

			if len(randomWords) == 0 {
				http.Error(w, "wordlist not loaded", http.StatusInternalServerError)
				return
			}

			randomBytes := make([]byte, 32) // Generate 256 bits entropy (32 * 8)
			_, err := rand.Read(randomBytes)
			if err != nil {
				http.Error(w, "entropy failure", http.StatusInternalServerError)
				return
			}

			hash := sha256.Sum256(randomBytes) // Optional scramble layer (sha256 calculator)
			n := new(big.Int).SetBytes(hash[:])
			var wordsout []string // Extract words
			zero := big.NewInt(0)
			counter := 0

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// fixed word count
				for n.Sign() > 0 && n.Cmp(zero) > 0 && counter <= quantity-1 {
					//mod := new(big.Int)
					//n.DivMod(n, base, mod)
					//wordsout = append(wordsout, randomWords[mod.Int64()])
					wordsout = append(wordsout, randomWords[counter])
					counter++
				}

				/*
					fmt.Println("Number of entries:", len(randomWords))
					fmt.Println("URL parameter words:", quantity)
					fmt.Println("Function output words:", counter)
				*/

				frame := EntropyFrame{
					Words: join(wordsout, " "),
					Hash:  hex.EncodeToString(hash[:]),
				}

				conn.SetWriteDeadline(time.Now().Add((refresh + 2000) * time.Millisecond))
				err := conn.WriteJSON(frame)
				if err != nil {
					return
				}
				atomic.AddUint64(&wssPayloads, +1)
				atomic.AddUint64(&rngBytesGenerated, uint64(len(wordsout)))
			default:
				continue
				//log.Println("Nothing to see here")
			}
		}
	}
}

func entropyWordHandler(d *rng.DRBG, quantity int, refreshRate int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		words := diceware.GetWords()
		randomWords := diceware.GetRandomWords()
		//wordsMap := diceware.GetWordsMap()

		randomBytes := make([]byte, 32) // Generate 256 bits random
		_, err := rand.Read(randomBytes)
		if err != nil {
			http.Error(w, "entropy failure", http.StatusInternalServerError)
			return
		}

		hash := sha256.Sum256(randomBytes) // Optional scramble layer

		if len(words) == 0 {
			http.Error(w, "wordlist not loaded", http.StatusInternalServerError)
			return
		}

		n := new(big.Int).SetBytes(hash[:]) // Convert to big.Int
		base := big.NewInt(int64(len(randomWords)))

		var wordsout []string
		zero := big.NewInt(0)
		counter := 0
		maxWords := quantity

		for n.Sign() > 0 && n.Cmp(zero) > 0 && counter < maxWords {
			mod := new(big.Int)
			n.DivMod(n, base, mod)
			wordsout = append(wordsout, randomWords[counter])
			counter++
		}

		hashHex := hex.EncodeToString(hash[:]) // Prepare output

		tmpl := `
<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<meta http-equiv="refresh" content="{{.Refresh}}">
<style>
body {
	background-color: black;
	color: #00ff88;
	font-family: monospace;
	text-align: center;
	padding-top: 10%;
}
.words {
	font-size: 2em;
	letter-spacing: 2px;
}
.hash {
	margin-top: 30px;
	font-size: 0.8em;
	color: #444;
}
</style>
</head>
<body>
<div class="words">{{.Words}}</div>
<div class="hash">{{.Hash}}</div>
</body>
</html>
`

		data := struct {
			Words   string
			Hash    string
			Refresh int
		}{
			Words:   template.HTMLEscapeString(join(wordsout, " ")),
			Hash:    hashHex,
			Refresh: refreshRate,
		}

		t := template.Must(template.New("page").Parse(tmpl))
		t.Execute(w, data)
		atomic.AddUint64(&rngBytesGenerated, uint64(len(wordsout)))
		atomic.AddUint64(&httpRequests, +1)
		//rng.DecreaseActiveInstances(-1)
	}
}

func randomBytesHandler(d *rng.DRBG, maxSize int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.WriteHeaders(w)
		w.Header().Set("Content-Type", "application/octet-stream")

		// Derive 32 bytes from master
		seed, _ := d.Derive(32)

		// Create per-request DRBG
		child, _ := rng.NewDRBG(seed)
		//child, _ := rng.Context().Value("conn_drbg").(*rng.DRBG)

		size := 65536
		if q := r.URL.Query().Get("bytes"); q != "" {
			//if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 1<<20
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= maxSize {
				size = v
			} else {
				size = maxSize
			}
		}
		buf := make([]byte, size)
		//buf := bufPool.Get().([]byte)
		//defer bufPool.Put(buf)
		//data := d.ReadInto(buf[:size]) //n
		//io.Reader(buf)
		//d.WriteTo(w, size)
		//w.Write(data)
		//io.Copy(w, buf)

		child.Read(buf)
		atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
		atomic.AddUint64(&httpRequests, +1)

		w.Write(buf)
	}
}

func fileAnalyzeHandler(d *rng.DRBG, fingerprint int, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		atomic.AddUint64(&httpRequests, +1)
		conn, cerr := upgrader.Upgrade(w, r, nil)
		if cerr != nil {
			log.Println("ws upgrade failed")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer conn.Close()
		defer ticker.Stop()

		if x := r.URL.Query().Get("refresh"); x != "" {
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 1<<20 {
				dtime := time.Duration(z)
				refresh = dtime
				//log.Printf("Nothing to see here: %d", dtime)
			}
		}

		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})
		conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			{
				bytes, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "Failed to read bytes", http.StatusBadRequest)
					return
				}

				buf := make([]byte, len(bytes))
				d.Read(bytes)

				// two options available here, raw encode or b64 encode
				base64 := base64.StdEncoding.EncodeToString(buf)
				//conv := hex.EncodeToString(buf)
				hash := sha256.Sum256(buf)

				//w.Header().Set("Content-Type", "application/octet-stream")
				//w.Header().Set("X-Entropy-Metric", "random-data-websocket")
				//w.Header().Set("X-RNG-Reseed-Age-ms-test",
				//	strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

				//frame := processBytes(bytes)
				frame := EntropyDataFrame{
					Hex:    hex.EncodeToString(buf[:]),
					Base64: base64,
					Hash:   hex.EncodeToString(hash[:]),
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(frame)

				cnerr := conn.WriteJSON(frame)
				if cnerr != nil {
					return
				}
				atomic.AddUint64(&wssPayloads, +1)
				atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
				//rng.DecreaseActiveInstances(-1)
				//continue
				//log.Println("Nothing to see here")
			}

		default:
			{
			}
		}
	}
}

func uploadHandler(d *rng.DRBG, fingerprint int, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		atomic.AddUint64(&httpRequests, +1)
		conn, cerr := upgrader.Upgrade(w, r, nil)
		if cerr != nil {
			log.Println("ws upgrade failed")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer conn.Close()
		defer ticker.Stop()

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		err := r.ParseMultipartForm(32 << 20) // 32MB max
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			{
				buffer := make([]byte, 4096)
				//for {
				n, err := file.Read(buffer)
				if n > 0 {
					//processChunk(buffer[:n]) // reuse your pipeline
					//d.Read(n)

					// two options available here, raw encode or b64 encode
					//base64 := base64.StdEncoding.EncodeToString(n)
					//conv := hex.EncodeToString(buf)
					//hash := sha256.Sum256(n[:])

					//w.Header().Set("Content-Type", "application/octet-stream")
					//w.Header().Set("X-Entropy-Metric", "random-data-websocket")
					//w.Header().Set("X-RNG-Reseed-Age-ms-test",
					//	strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

					//frame := processBytes(bytes)
					/*
						frame := EntropyDataFrame{
							Hex:    hex.EncodeToString(n[:]),
							Base64: base64,
							Hash:   hex.EncodeToString(n[:]),
						}
					*/

					conn.WriteMessage(websocket.BinaryMessage, buffer[:n])

					w.Header().Set("Content-Type", "application/json")
					//json.NewEncoder(w).Encode(frame)

					//cnerr := conn.WriteJSON(frame)
					//if cnerr != nil {
					//	return
					//}
					atomic.AddUint64(&wssPayloads, +1)
					//atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
					//rng.DecreaseActiveInstances(-1)
					//continue
					//log.Println("Nothing to see here")
					//}

					if err == io.EOF {
						break
					}
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				w.WriteHeader(http.StatusOK)
			}
		}
	}
}

func wsBytesHandler(d *rng.DRBG, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		atomic.AddUint64(&httpRequests, +1)
		conn, cerr := upgrader.Upgrade(w, r, nil)
		if cerr != nil {
			log.Println("ws upgrade failed")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer conn.Close()
		defer ticker.Stop()

		n := 2048

		if q := r.URL.Query().Get("bytes"); q != "" {
			if v, verr := strconv.Atoi(q); verr == nil && v > 0 && v <= 1<<20 {
				n = v
			}
		}

		if x := r.URL.Query().Get("refresh"); x != "" {
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 1<<20 {
				dtime := time.Duration(z)
				refresh = dtime
				//log.Printf("Nothing to see here: %d", dtime)
			}
		}

		// read cycle, to detect ghost clients and ensure proper close
		go func() {
			defer cancel()

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					//ctx.Done() <- 0
					break
				}
			}
		}()

		for {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			/*
				if n < 64 { return }
				if first == 1 { first = 0 }
				if buf == nil {
					http.Error(w, "failed to create buf", http.StatusInternalServerError)
					return
				}
			*/

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				buf := make([]byte, n)
				d.Read(buf)
				// two options available here, raw encode or b64 encode
				base64 := base64.StdEncoding.EncodeToString(buf)
				//conv := hex.EncodeToString(buf)
				hash := sha256.Sum256(buf)

				frame := EntropyDataFrame{
					Hex:    hex.EncodeToString(buf[:]),
					Base64: base64,
					Hash:   hex.EncodeToString(hash[:]),
				}

				//w.Header().Set("Content-Type", "application/octet-stream")
				//w.Header().Set("X-Entropy-Metric", "random-data-websocket")
				//w.Header().Set("X-RNG-Reseed-Age-ms-test",
				//	strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

				err := conn.WriteJSON(frame)
				if err != nil {
					return
				}
				atomic.AddUint64(&wssPayloads, +1)
				atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
				//rng.DecreaseActiveInstances(-1)
			default:
				{
					continue
					//log.Println("Nothing to see here")
				}

			}
		}
	}
}

func wsBinaryHandler(d *rng.DRBG, refresh time.Duration, quantity int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		atomic.AddUint64(&httpRequests, +1)

		conn, cerr := upgrader.Upgrade(w, r, nil)
		if cerr != nil {
			log.Println("ws upgrade failed")
			return
		}

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer conn.Close()
		defer ticker.Stop()

		if q := r.URL.Query().Get("bytes"); q != "" {
			if v, verr := strconv.Atoi(q); verr == nil && v > 0 && v <= 1<<20 {
				quantity = v
			}
		}

		if x := r.URL.Query().Get("refresh"); x != "" {
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 1<<20 {
				dtime := time.Duration(z)
				refresh = dtime
				//log.Printf("Nothing to see here: %d", dtime)
			}
		}

		// read cycle, to detect ghost clients and ensure proper close
		go func() {
			// cancel context when read fails
			defer cancel()

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					break
				}
			}
		}()

		for {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, quantity)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.Read(buf)
				err := conn.WriteMessage(websocket.BinaryMessage, buf)
				if err != nil {
					return
				}
				atomic.AddUint64(&wssPayloads, +1)
				atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
				//rng.DecreaseActiveInstances(-1)
			default:
				{
					continue
					//log.Println("Nothing to see here")
				}
			}
		}
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
		payloads := atomic.LoadUint64(&wssPayloads)
		entropy := atomic.LoadUint64(&rngBytesBuffered)
		entropyA := atomic.LoadUint64(&rngBytesTestA)
		entropyB := atomic.LoadUint64(&rngBytesTestB)
		DRBGcnt := metrics.DRBGInstanceCnt

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

# HELP wss_payloads_total Total WSS Payloads sent within sessions
# TYPE wss_payloads counter
wss_payloads %d

# HELP entropy_buffer_size_kb Total size of entropy buffer
# TYPE entropy_buffer_size_kb gauge
entropy_buffer_capacity_kb %d

# HELP entropy_buffer_capacity_kb_A Total size of entropy buffer TEST A
# TYPE entropy_buffer_capacity_kb_A gauge
entropy_buffer_capacity_kb_A %d

# HELP entropy_buffer_capacity_kb_B Total size of entropy buffer TEST B
# TYPE entropy_buffer_capacity_kb_B gauge
entropy_buffer_capacity_kb_B %d

# HELP drbg_instance_count Total number of DBGG instances 
# TYPE drbg_instance_count counter
drbg_instance_count %d
`,
			bytes,
			reseeds,
			age,
			bufBytes,
			bufCap,
			reqs,
			payloads,
			entropy,
			entropyA,
			entropyB,
			DRBGcnt,
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
			EntropyBufferedBytes: meta.EntropyBufferedBytes,
			EntropyBufferedPCT:   meta.EntropyFillPct,
		}

		d.WriteHeaders(w)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(health); err != nil {
			http.Error(w, "failed to encode health info", http.StatusInternalServerError)
		}
	}
}

func deviceExists(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("device %s does not exist", path)
		}
		return fmt.Errorf("error accessing device %s: %w", path, err)
	}
	return nil
}

func validateDevice(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	mode := info.Mode()

	if mode&os.ModeDevice == 0 {
		return fmt.Errorf("%s exists but is not a device file", path)
	}

	return nil
}

func testDeviceReadable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open device %s: %w", path, err)
	}
	defer f.Close()
	return nil
}

func validateEntropyDevice(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	//if mode&os.ModeCharDevice == 0
	if info.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s is not a character device", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", path, err)
	}
	defer f.Close()

	return nil
}

func ResolveEntropyDevice(path string, require bool) (string, error) {

	// If no device specified use kernel CSPRNG
	if path == "" {
		return DefaultCSPRNG, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if require {
			return "", fmt.Errorf("required device not available: %w", err)
		}
		log.Printf("Device %s unavailable, falling back to /dev/urandom\n", path)
		return DefaultCSPRNG, nil
	}

	if info.Mode()&os.ModeCharDevice == 0 {
		if require {
			return "", fmt.Errorf("%s is not a character device", path)
		}
		log.Printf("%s is not a device, falling back to /dev/urandom\n", path)
		return DefaultCSPRNG, nil
	}

	return path, nil
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
		Addr:         addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		Handler:      handler,
		ConnContext: func(cctx context.Context, c net.Conn) context.Context {
			//seed, _ := master.Derive(32)
			//nonce, _ := master.Derive(12)
			// derive per-connection DRBG from master
			//childDRBG, _ := rng.NewDRBG(seed)
			childDRBG, childerr := rng.NewConnectionDRBG(master) // (DRBG)
			if childerr != nil {
				return ctx
			}
			//rng.DecreaseActiveInstances(-1)

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
	ln, err := newTunedListener(addr, 4<<20)
	if err != nil {
		return nil, err
	}

	//cert, err := tls.LoadX509KeyPair(CertFile, KeyFile)
	//tlsConfig.Certificates = []tls.Certificate{cert}
	tlsConfig.ClientAuth = tls.NoClientCert
	tlsLn := tls.NewListener(ln, tlsConfig)

	srv := &http.Server{
		Addr:         addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		Handler:      handler,
		TLSConfig:    tlsConfig,
		ConnContext: func(cctx context.Context, c net.Conn) context.Context {
			childDRBG, cerr := rng.NewConnectionDRBG(master) // (DRBG)
			if cerr != nil {
				return ctx
			}
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

	cfg := ParseConfig()

	if cfg.ReseedMs <= 0 || cfg.ReseedMs > 10001 {
		panic("reseed-ms must be between 1 and 10000 msec")
	}

	if cfg.MaxBytes > 2097153 {
		panic("max-bytes must be < 2097152")
	}

	if cfg.QRNGBuffer < 1 || cfg.QRNGBuffer > 4097 {
		panic("QRNG buffer size KB must be between 1 and 4096 KB")
	}

	fmt.Println("---")
	fmt.Println("HTTP port:", cfg.HTTPAddr)
	fmt.Println("HTTPS port:", cfg.HTTPSAddr)
	fmt.Println("CertFile TLS:", cfg.CertFile)
	fmt.Println("KeyFile TLS:", cfg.KeyFile)
	fmt.Println("EnableHTTPS flag:", cfg.EnableHTTPS)
	fmt.Println("---")
	fmt.Println("Reseed size:", cfg.ReseedSize)
	fmt.Println("ReseedMs interval:", cfg.ReseedMs)
	fmt.Println("Max request size KB:", cfg.MaxBytes/1024)
	fmt.Println("QRNGBuffer size:", cfg.QRNGBuffer)
	fmt.Println("Reseed Buffer size:", cfg.SeedBuffer)
	fmt.Println("---")
	fmt.Println("Max number of Bytes:", cfg.MaxBytes)
	fmt.Println("Maximum number of Words:", cfg.MaxWords)
	fmt.Println("---")
	fmt.Println("Refresh Rate (seconds) value:", cfg.RefreshRate)
	fmt.Println("RefreshMs (ms) value:", cfg.RefreshRateMs)
	fmt.Println("RefreshColorMs (ms) value:", cfg.RefreshColorMs)
	fmt.Println("---")

	//check for entropy source availability and access rights
	dev, err := ResolveEntropyDevice(cfg.DevicePath, cfg.RequireDevice)
	if err != nil {
		log.Fatal(err)
	}
	if derr := deviceExists(dev); derr != nil {
		log.Fatal(derr)
	}
	if verr := validateEntropyDevice(dev); verr != nil {
		log.Fatal("Entropy device validation failed: ", verr)
	}
	if dev == "/dev/urandom" {
		log.Println("Entropy mode: kernel CSPRNG fallback")
	} else {
		log.Println("Entropy mode: hardware device:", dev)
	}
	log.Printf("Entropy source: %s\n", dev)

	// Initialize QRNG buffer
	//entropy := NewEntropySource(cfg.DevicePath)
	//qrngBuf := rng.NewQRNGBuffer(entropy, cfg.BufferKB*1024)
	qrngBuf := rng.NewQRNGBuffer(dev, cfg.QRNGBuffer*1024)

	// pass entropy along
	//master := NewMasterDRBG(entropy)

	// Initialize seed space (in bytes here)
	seed, serr := fetchEntropy(64, dev, cfg.SeedBuffer) // 64*8 = 512 bits
	//seed, serr := fetchEntropy(cfg.SeedBuffer, dev, cfg.QRNGBuffer) // 64*8 = 512 bits
	if serr != nil {
		log.Fatal(serr)
	}

	// Initialize DRBG. Note that multiple instances of DRBG are created on a per-connection basis
	drbg, derr := rng.NewDRBG(seed)
	if derr != nil {
		log.Fatal(derr)
	}

	//drbg.SetMetadata("1.0.0", "QRNG-idQuantique-QuantisPCI", "ChaCha20", time.Duration(cfg.ReseedMs)*time.Millisecond, cfg.ReseedSize, qrngBuf)
	drbg.SetMetadata("1.1.0", dev, "ChaCha20", time.Duration(cfg.ReseedMs)*time.Millisecond, cfg.ReseedSize, qrngBuf)

	// Attach the QRNG buffer for dynamic header reporting
	drbg.SetEntropyBuffer(qrngBuf)

	//tln := newTunedListener(ln)
	tlsCfg := newTLSConfig(cfg.CertFile, cfg.KeyFile)
	cert, crterr := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if crterr != nil {
		log.Fatal(crterr)
	}
	tlsCfg.Certificates = []tls.Certificate{cert}
	tlsCfg.ClientAuth = tls.NoClientCert

	masterDRBG, _ := rng.NewDRBG(seed)

	// create the multiplexed listener proto
	mux := http.NewServeMux()

	//fs := http.FS(webFS)
	//http.Handle("/", http.FileServer(fs))
	//mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "./web/index.html")})

	mux.HandleFunc("/bytes", wsBytesHandler(drbg, cfg.RefreshRateMs))
	mux.HandleFunc("/stream", wsBinaryHandler(drbg, cfg.RefreshColorMs, 2048))
	mux.HandleFunc("/files", uploadHandler(drbg, cfg.BytesFingerprint, cfg.RefreshColorMs)) // cfg.MaxBytes
	//mux.HandleFunc("/files", fileAnalyzeHandler(drbg, cfg.BytesFingerprint, cfg.RefreshColorMs)) // cfg.MaxBytes
	mux.HandleFunc("/colors", wsBytesHandler(drbg, cfg.RefreshColorMs))
	mux.HandleFunc("/words", wsWordsHandler(drbg, cfg.MaxWords, cfg.RefreshColorMs))
	mux.Handle("/", http.FileServer(http.Dir("./web")))

	mux.HandleFunc("/v1/data/random", randomBytesHandler(drbg, cfg.MaxBytes))
	mux.HandleFunc("/v1/data/test", randomHandler(drbg))
	mux.HandleFunc("/v1/image/random", randomImageHandler(drbg))
	mux.HandleFunc("/v1/image/heatmap", entropyHeatmapHandler(drbg))
	mux.HandleFunc("/v1/meta/random", entropyWordHandler(drbg, cfg.MaxWords, cfg.RefreshRate))
	mux.HandleFunc("/paroleparoleparole", entropyWordHandler(drbg, cfg.MaxWords, cfg.RefreshRate))
	// placeholder for QR-codes generation
	//mux.HandleFunc("/v1/qr/random", healthHandler(drbg))
	// placeholder for public/private key generation
	//mux.HandleFunc("/v1/cert/random", healthHandler(drbg))
	mux.HandleFunc("/health", healthHandler(drbg))
	mux.Handle("/metrics", metricsHandler(drbg))

	// wait for first reseed to fully complete, especially useful when using fallback CSPRNG
	//time.Sleep(time.Duration(cfg.ReseedMs))

	// TODO: fix logic here
	// Run permanent reseed loop
	//go reseedLoop(ctx, drbg, cfg.ReseedMs, dev, cfg.BufferSize, cfg.SeedBuffer)
	go reseedLoop(ctx, drbg, cfg.ReseedMs, dev, cfg.QRNGBuffer, cfg.SeedBuffer)

	// context was here
	//ctx, cancel := context.WithCancel(context.Background())
	//defer cancel()

	var httpSrv *http.Server
	var httpsSrv *http.Server
	var httpErr error
	var httpsErr error

	// start HTTP & HTTPS servers on the same mux
	httpSrv, httpErr = startHTTP(ctx, cfg.HTTPAddr, mux, masterDRBG)
	if httpErr != nil {
		log.Fatal(httpErr)
	}

	//if os.Getenv("TLS") == "1"
	if cfg.EnableHTTPS == true {
		httpsSrv, httpsErr = startHTTPS(ctx, cfg.HTTPSAddr, mux, tlsCfg, masterDRBG)
		if httpsErr != nil {
			log.Fatal(httpsErr)
		}
	}

	log.Println("HTTP server running on", cfg.HTTPAddr)
	if cfg.EnableHTTPS == true {
		log.Println("HTTPS server running on", cfg.HTTPSAddr)
	}

	<-ctx.Done()

	log.Println("shutdown signal received")

	//shutdownCtx, cancel := context.WithCancel(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Block and wait for shutdown
	if httpErr != nil {
		_ = httpSrv.Shutdown(shutdownCtx)
	}

	//if cfg.HTTPSAddr != ""
	if cfg.EnableHTTPS == true {
		if httpsErr != nil {
			_ = httpsSrv.Shutdown(shutdownCtx)
		}
	}

	log.Println("shutdown complete")
}

/*
func entropyJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	frame := generateEntropyFrame(8) // reuse your projection logic

	json.NewEncoder(w).Encode(frame)
}

type EntropyFrame struct {
	Words string `json:"words"`
	Hash  string `json:"hash"`
}
*/
