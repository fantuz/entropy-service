package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	// "golang.org/x/net/http2" // enable HTTP/2.
	"github.com/8ff/diceware"
	// "github.com/fantuz/entropy-service/cmd/entropy-server/internal/protocol".
	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/listener"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/qrng"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/transport"
)

type ClientPrefs struct {
	Bytes   int `json:"bytes"`
	Refresh int `json:"refresh"`
	Words   int `json:"words"`
}

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const connDRBGKey contextKey = "conn_drbg"

type EntropyPool struct {
	buf      []byte
	size     int
	readPos  int
	writePos int
	// mu       sync.Mutex
	cond *sync.Cond
}

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

// Buffered QRNG struct.
type QRNGBuffer struct {
	buf       []byte
	mu        sync.Mutex
	fillDelay time.Duration
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

const (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[37m"
	White   = "\033[97m"
)

var qrngBuffer QRNGBuffer

var entropyPool EntropyPool

var bufPool = sync.Pool{
	New: func() any {
		return make([]byte, 1<<25) // 32 MB
	},
}

/*
func ActiveInstances() int64 {
	return activeDRBG.Load()
}
*/

func setPrefsCookie(w http.ResponseWriter, prefs ClientPrefs) error {
	data, err := json.Marshal(prefs)
	if err != nil {
		return err
	}

	// Non-sensitive UI preferences. Secure is intentionally left unset so the
	// cookie also works when the server is reached over plain HTTP (the
	// default when -enable-https=false).
	cookie := &http.Cookie{
		Name:     "entropy_prefs",
		Value:    base64.StdEncoding.EncodeToString(data),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400, // 1 hour
	}

	http.SetCookie(w, cookie)

	return nil
}

func getPrefsCookie(r *http.Request) (*ClientPrefs, error) {
	c, err := r.Cookie("entropy_prefs")
	if err != nil {
		return nil, err
	}

	raw, err := base64.StdEncoding.DecodeString(c.Value)
	if err != nil {
		return nil, err
	}

	var prefs ClientPrefs
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return nil, err
	}

	return &prefs, nil
}

func extractPrefs(r *http.Request) ClientPrefs {
	q := r.URL.Query()

	prefs := ClientPrefs{
		Bytes:   524288, // 2097152
		Refresh: 500,
		Words:   3,
	}

	// try cookie first
	c, cerr := getPrefsCookie(r)
	if cerr == nil {
		prefs = *c
	}

	// override with query params if present
	if v := q.Get("bytes"); v != "" {
		prefs.Bytes, _ = strconv.Atoi(v)
	}

	if v := q.Get("refresh"); v != "" {
		prefs.Refresh, _ = strconv.Atoi(v)
	}

	if v := q.Get("words"); v != "" {
		prefs.Words, _ = strconv.Atoi(v)
	}

	// log.Printf("Cookie Bytes: %v\n", prefs.Bytes)
	// log.Printf("Cookie Refresh: %v\n", prefs.Refresh)
	// log.Printf("Cookie Words: %v\n", prefs.Words)
	return prefs
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func popcount(b byte) int {
	b -= (b >> 1) & 0x55
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

    f, err := os.Open("/dev/qrandom0")
    if err != nil { return nil, err }
    defer f.Close()

    total := 0
    for total < n {
    	//_, err := rand.Read(b) // replace with QRNG logic
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

func NewEntropyPool(size int) EntropyPool {
	// p := EntropyPool
	p := &EntropyPool{
		buf:  make([]byte, size),
		size: size,
	}

	// p.cond = sync.NewCond(&p.mu)

	return *p
	// return p
}

func (p *EntropyPool) Write(data []byte) {
	// p.mu.Lock()
	// defer p.mu.Unlock()
	for _, b := range data {
		next := (p.writePos + 1) % p.size

		if next == p.readPos {
			// buffer full
			p.cond.Wait()

			continue
		}

		p.buf[p.writePos] = b
		p.writePos = next
	}

	p.cond.Broadcast()
}

func (p *EntropyPool) Read(n int) []byte {
	out := make([]byte, n)

	// p.mu.Lock()
	// defer p.mu.Unlock()

	for i := range n {
		/*
			for p.readPos == p.writePos {
				p.cond.Wait()
			}
		*/
		out[i] = p.buf[p.readPos]
		p.readPos = (p.readPos + 1) % p.size
	}

	//	p.cond.Broadcast()

	return out
}

func startEntropyProducer(pool EntropyPool, dev string, quantity int) {
	go func() {
		tmp := make([]byte, 4096)

		for {
			generateEntropy(tmp, dev, quantity)
			pool.Write(tmp)
		}
	}()
}

func NewEntropySource(path string, require bool) (EntropySource, error) {
	if path == "" {
		if require {
			return nil, errors.New("device required but not specified")
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

/*
func initQRNGBuffer(dev string, l int) {
	// size in MB
	//qrngBuffer = NewQRNGBuffer(dev, l*1024*1024)
	qrngBuffer = qrng.NewQRNGBuffer(dev, l)
	// report in KB
	log.Printf("initQRNG() set qrngBuffer to %v KB", l*1024*1024)
	// Attach it to DRBG
	//SetEntropyBuffer(qrngBuffer)
}
*/

// NewQRNGBuffer creates a buffered QRNG reader
/*
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
	// TODO: fix here
	//metrics.AddBufferedBytes(uint64(q.capacity))
	metrics.AddBufferedBytes(uint64(q.capacity))
	//incBuffer()

	return q
}
*/

// Get returns n bytes from the buffer (blocking if necessary).
func (q *QRNGBuffer) Get(n int) ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Wait for buffer to fill if necessary
	for len(q.buf) < n {
		// q.mu.Unlock()
		time.Sleep(q.fillDelay)
		// q.mu.Lock()
	}

	out := q.buf[:n]
	q.buf = q.buf[n:]
	// TODO restore: incTestB(len(q.buf))
	metrics.AddBufferedBytes(uint64(len(q.buf)))

	return out, nil
}

func generateEntropy(buf []byte, dev string, quantity int) {
	// _, err := io.ReadFull(drbgreal, buf)
	// if err != nil {
	//    log.Printf("entropy error: %v", err)
	// }
	data, err := fetchEntropy(len(buf), dev, quantity)
	// data, err := fetchEntropy(len(buf),DefaultCSPRNG,quantity)
	fmt.Println("entropy generate done")

	if err != nil {
		log.Printf("entropy error: %v", err)

		return
	}

	copy(buf, data)
}

/*
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
			time.Sleep(10 * time.Millisecond) // retry
			continue
		}

		total := 0
		for total < free {
			m, err := f.Read(tmp[total:])
			if err != nil {
				break
			}
			total += m
			// TODO restore: incTestA(m)
			metrics.AddBufferedBytes(uint64(m))
		}
		f.Close()

		q.mu.Lock()
		q.buf = append(q.buf, tmp[:total]...)
		q.mu.Unlock()
	}
}
*/

/*
// fetchEntropySimple reads n bytes from the buffered QRNG
func fetchEntropySimple(n int, dev string, bufferlen int) ([]byte, error) {
	log.Println("entropy fetch start")
	if &qrngBuffer != nil {
		// init QRNG
		log.Println("entropy fetch inside 1")
		qrng.InitQRNGBuffer(dev, bufferlen)
		log.Println("entropy fetch inside 2")

		// Attach it to DRBG
		//drbg.SetEntropyBuffer(&qrngBuffer)
		//SetEntropyBuffer(qrng.QRNGBuffer)
	}

	//metrics.AddBufferedBytes(uint64(q.capacity))
	//atomic.AddUint64(&rngBufferSize, uint64(len(qrngBuffer)))
	return qrng.Read(n)
}
*/

// fetchEntropy reads n bytes from the buffered QRNG.
func fetchEntropy(n int, dev string, bufferlen int) ([]byte, error) {
	log.Println("entropy fetch start")

	// NOTE: previously this init was wrapped in `if &qrngBuffer != nil { ... }`.
	// That guard was dead code: qrngBuffer is a package-level variable, so its
	// address is never nil and the check was always true (staticcheck SA4022).
	// The guard was removed; the init below runs unconditionally, exactly as it
	// did before.
	// init QRNG
	log.Println("entropy fetch inside 1")
	qrng.InitQRNGBuffer(dev, bufferlen)
	log.Println("entropy fetch inside 2")

	// TODO: fix here
	// metrics.AddBufferedBytes(uint64(q.capacity))
	// atomic.AddUint64(&rngBufferSize, uint64(len(qrngBuffer)))
	return qrngBuffer.Get(n)
}

// func reseedLoop(ctx context.Context, d *drbg.DRBG, interval int, dev string, bufferlen int, reseedbuf int).
func reseedLoop(ctx context.Context, d *drbg.DRBG, interval int, _ string, bufferlen int, reseedbuf []byte) {
	// log.Printf("reseedLoop started for %q", d)
	// log.Printf("reseedLoop started for %v", d)
	// log.Println("reseed interval:", interval)
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	// ticker := time.NewTicker(time.Duration(interval))
	defer ticker.Stop()
	// fmt.Printf("%v\n", ticker)

	// ctx, cancel := context.WithCancel(context.Background())
	// ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	// defer cancel()

	/*
		select {
			case <-ctx.Done():
				log.Println("reseedLoop: context already canceled")
				go func() {
				    //sig := <-signalChan
				    //sig := <-ctx
				    //log.Println("shutting down:", sig)
				    cancel()
				}()
			default:
				//continue
		}
	*/

	for {
		select {
		case <-ctx.Done():
			log.Println("ctx done immediately")
			// sig := <-ctx
			// ctx.cancel()
			return
		case <-ticker.C:
			// for range ticker.C {
			// log.Printf("reseedLoop: started, bufferlen: %d, reseedbuf: %d", bufferlen, reseedbuf)

			/*
				entropy, eerr := fetchEntropy(bufferlen, dev, reseedbuf)
				//entropy, _ := fetchEntropy(reseedbuf, dev, bufferlen)

				if eerr != nil {
					log.Println("entropy fetch failed:", eerr)
					//continue
				}
			*/
			ticker.Stop()

			buf := make([]byte, reseedbuf[bufferlen])

			eerr := d.Reseed(buf)
			if eerr != nil {
				log.Println("reseed failed:", eerr)
			}

			metrics.AddReseeds(1)

			ctx.Done()
			// log.Println("reseedLoop: fetch done")
			return
			//}
		default:
			// ticker.Stop()
			// defer ticker.Stop()
			// return
			continue
		}
		// log.Println("interval:", interval)
	}
}

func entropyHeatmapHandler(d *drbg.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		width := 1024
		height := 1024

		img := image.NewRGBA(image.Rect(0, 0, width, height))

		buf := make([]byte, width*height)
		_, _ = d.Read(buf)

		i := 0

		for y := range height {
			row := y * img.Stride

			for x := range width {
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
		_ = png.Encode(w, img)
	}
}

// qr?bytes=32&size=256&format=hex.
func qrHandler(d *drbg.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		/*
			size := 256
			if s := r.URL.Query().Get("size"); s != "" {
			    size, _ = strconv.Atoi(s)
			}
		*/

		// 1. get entropy
		buf := make([]byte, 64)
		_, _ = d.Read(buf)

		// 2. encode (hex or base64 recommended)
		data := hex.EncodeToString(buf)

		// 3. generate QR
		png, err := qrcode.Encode(data, qrcode.Medium, 256)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		// 4. return image
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
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

func randomImageHandler(d *drbg.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		width := 1024
		height := 1024

		img := image.NewRGBA(image.Rect(0, 0, width, height))

		// Fill the entire backing buffer with DRBG output
		_, _ = d.Read(img.Pix)

		// Force alpha channel to opaque
		for y := range height {
			row := y * img.Stride
			for x := range width {
				img.Pix[row+x*4+3] = 255
			}
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Refresh", "5")
		w.Header().Set("X-Entropy-Metric", "random-image")
		w.Header().Set("X-RNG-Reseed-Age-ms-test",
			strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))
		_ = png.Encode(w, img)
		metrics.AddBytesGenerated(len(img.Pix))
		metrics.AddHTTPRequests(1)
	}
}

func randomHandler(d *drbg.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := 1024

		if q := r.URL.Query().Get("bytes"); q != "" {
			v, verr := strconv.Atoi(q)
			if verr == nil && v > 0 && v <= 1<<25 {
				n = v
			}
		}

		buf := make([]byte, n)
		// TODO: fixme
		// d.Read(buf[:n])
		_, _ = d.Read(buf[n:])

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Entropy-Metric", "random-data")
		w.Header().Set("X-RNG-Reseed-Age-ms-test",
			strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))
		_, _ = w.Write(buf)
	}
}

func join(words []string, sep string) string {
	var result strings.Builder

	for i, w := range words {
		if i > 0 {
			result.WriteString(sep)
		}

		result.WriteString(w)
	}

	return result.String()
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

func wsWordsHandler(quantity int, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(refresh * time.Millisecond)

		metrics.AddHTTPRequests(1)

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
			v, verr := strconv.Atoi(q)
			if verr == nil && v > 0 && v <= 7776 {
				quantity = v
			}
		}

		if x := r.URL.Query().Get("refresh"); x != "" {
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 1<<25 {
				dtime := time.Duration(z)
				refresh = dtime
			}
		}

		// read cycle, to detect ghost clients and ensure proper close
		go func() {
			defer cancel()

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					// ctx.Done() <- 0
					break
				}
			}
			// quit <- 0
		}()

		for {
			randomWords := diceware.GetRandomWords()
			// base := big.NewInt(int64(len(randomWords)))

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

			// Extract words
			var wordsout []string

			zero := big.NewInt(0)
			counter := 0

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// fixed word count
				for n.Sign() > 0 && n.Cmp(zero) > 0 && counter <= quantity-1 {
					// mod := new(big.Int)
					// n.DivMod(n, base, mod)
					// wordsout = append(wordsout, randomWords[mod.Int64()])
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

				_ = conn.SetWriteDeadline(time.Now().Add((refresh + 2000) * time.Millisecond))

				err := conn.WriteJSON(frame)
				if err != nil {
					return
				}

				metrics.AddBytesGenerated(len(wordsout))
				metrics.AddWSPayloads(1)
			default:
				// log.Println("Nothing to see here")
				continue
			}
		}
	}
}

func entropyWordHandler(_ *drbg.DRBG, quantity int, refreshRate int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		words := diceware.GetWords()
		randomWords := diceware.GetRandomWords()
		// wordsMap := diceware.GetWordsMap()

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
		_ = t.Execute(w, data)

		metrics.AddBytesGenerated(len(wordsout))
		metrics.AddHTTPRequests(1)
		// rng.DecreaseActiveInstances(-1)
	}
}

func randomBytesHandler(d *drbg.DRBG, _ int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prefs := extractPrefs(r)
		_ = setPrefsCookie(w, prefs)

		d.WriteHeaders(w)
		w.Header().Set("Content-Type", "application/octet-stream")

		// Derive 32 bytes from master
		seed, _ := d.Derive(64)

		// Create per-request DRBG
		child, _ := drbg.NewDRBG(seed)
		// child, _ := qrng.Context().Value("conn_drbg").(*qrng.NewDRBG)

		size := 2097152

		if q := r.URL.Query().Get("bytes"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 1<<25 {
				size = v
			}
		} else {
			size = prefs.Bytes
		}

		buf := make([]byte, size)

		// buf := bufPool.Get().([]byte)
		// defer bufPool.Put(buf)
		// data := d.ReadInto(buf[:size]) //n
		// io.Reader(buf)
		// d.Read(buf[size:])
		// d.WriteTo(w, size)
		// io.Copy(w, buf)
		// child.Read(buf[:size])
		// x := child.Read(buf[:size])
		// aa := entropyPool.Read(size)

		_, _ = io.ReadFull(&child, buf)
		_, _ = w.Write(buf)

		metrics.AddBytesGenerated(len(buf))
		metrics.AddHTTPRequests(1)
	}
}

// NOTE: the drbg.DRBG and fingerprint parameters are currently blanked (_)
// because this handler's body does not use them yet. They were named before,
// which the linter flagged as unused parameters. The signature is kept intact
// (rather than dropping the params) so the /files call site and the
// http.HandlerFunc shape stay unchanged for when they get wired in.
// fileAnalyzeHandler is the WebSocket-based alternative to uploadHandler for the
// /files route (see the commented-out mux registration in main). It is kept for
// reference while the upload analysis path is finalized.
//
//nolint:unused // retained deliberately; wired via the commented-out /files route.
func fileAnalyzeHandler(d *drbg.DRBG, _ int, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(refresh * time.Millisecond)

		metrics.AddHTTPRequests(1)

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
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 1<<25 {
				refresh = time.Duration(z)
				// log.Printf("Nothing to see here: %d", refresh)
			}
		}

		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		})
		_ = conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))

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

				// TODO: add upload limiter, i.e. 50MB
				// r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

				buf := make([]byte, len(bytes))
				_, _ = d.Read(bytes)

				// two options available here, raw encode or b64 encode
				b64 := base64.StdEncoding.EncodeToString(buf)
				// conv := hex.EncodeToString(buf)
				hash := sha256.Sum256(buf)

				// w.Header().Set("Content-Type", "application/octet-stream")
				// w.Header().Set("X-Entropy-Metric", "random-data-websocket")
				// w.Header().Set("X-RNG-Reseed-Age-ms-test",
				//	strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

				// frame := processBytes(bytes)
				frame := EntropyDataFrame{
					Hex:    hex.EncodeToString(buf),
					Base64: b64,
					Hash:   hex.EncodeToString(hash[:]),
				}

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(frame)

				cnerr := conn.WriteJSON(frame)
				if cnerr != nil {
					return
				}

				metrics.AddBytesGenerated(len(buf))
				metrics.AddWSPayloads(1)
				// rng.DecreaseActiveInstances(-1)
				// continue
				// log.Println("Nothing to see here")
			}

		default:
			{
			}
		}
	}
}

func uploadHandler(_ *drbg.DRBG, _ int, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(refresh * time.Millisecond)

		metrics.AddHTTPRequests(1)

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
			// TODO: fix here
			// http: response.WriteHeader on hijacked connection from main.main.uploadHandler.func6 (entropy-server.go:936)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		// Bound the request body before parsing to avoid memory exhaustion.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<25) // 32 MB max

		err := r.ParseMultipartForm(1 << 25) // 32 MB max
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
			log.Println("context closed")

			return
		case <-ticker.C:
			{
				buffer := make([]byte, 4096)
				// for {
				n, err := file.Read(buffer)
				if n > 0 {
					// processChunk(buffer[:n]) // reuse your pipeline
					// d.Read(n)

					// w.Header().Set("Content-Type", "application/octet-stream")
					// w.Header().Set("X-Entropy-Metric", "random-data-websocket")
					// w.Header().Set("X-RNG-Reseed-Age-ms-test",
					//	strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

					// frame := processBytes(bytes)
					_ = conn.WriteMessage(websocket.BinaryMessage, buffer[:n])

					w.Header().Set("Content-Type", "application/json")
					// json.NewEncoder(w).Encode(frame)

					// cnerr := conn.WriteJSON(frame)
					// if cnerr != nil {
					// 	return
					// }
					// continue
					// log.Println("Nothing to see here")
					// }

					if errors.Is(err, io.EOF) {
						break
					}

					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)

						return
					}

					// metrics.AddBytesGenerated(len(buf))
					// atomic.AddUint64(&rngBytesGenerated, uint64(len(buf)))
					// rng.DecreaseActiveInstances(-1)
					metrics.AddWSPayloads(1)
					metrics.AddHTTPRequests(1)
				}

				w.WriteHeader(http.StatusOK)
			}
		default:
		}
	}
}

func wsBytesHandler(d *drbg.DRBG, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(refresh * time.Millisecond)

		metrics.AddHTTPRequests(1)

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
			v, verr := strconv.Atoi(q)
			if verr == nil && v > 0 && v <= 1<<25 {
				n = v
			}
		}

		if x := r.URL.Query().Get("refresh"); x != "" {
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 60000 {
				dtime := time.Duration(z)
				refresh = dtime
				// log.Printf("Nothing to see here: %d", dtime)
			}
		}

		// read cycle, to detect ghost clients and ensure proper close
		go func() {
			defer cancel()

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					// ctx.Done() <- 0
					break
				}
			}
		}()

		// buf := make([]byte, n)
		buf, _ := bufPool.Get().([]byte)

		if buf == nil {
			http.Error(w, "failed to create buf", http.StatusInternalServerError)

			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// bufPool.Put(buf)
				// defer bufPool.Put(buf)
				_, _ = io.ReadFull(d, buf)

				// d.Read(buf[:n]) // n
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

				// entropy, _ := io.ReadFull(&d, buf)

				// two options available here, raw encode or b64 encode
				base64 := base64.StdEncoding.EncodeToString(buf[:n]) // n
				// base64 := base64.StdEncoding.EncodeToString(entropy) //buf[:n])
				// conv := hex.EncodeToString(buf)
				hash := sha256.Sum256(buf)

				frame := EntropyDataFrame{
					Hex:    hex.EncodeToString(buf[:n]), // [;n}
					Base64: base64,
					Hash:   hex.EncodeToString(hash[:]),
				}

				// w.Header().Set("Content-Type", "application/octet-stream")
				// w.Header().Set("X-Entropy-Metric", "random-data-websocket")
				// w.Header().Set("X-RNG-Reseed-Age-ms-test",
				//	strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

				err := conn.WriteJSON(frame)
				if err != nil {
					return
				}

				metrics.AddBytesGenerated(len(buf))
				metrics.AddWSPayloads(1)
				// rng.DecreaseActiveInstances(-1)
			default:
				{
					// log.Println("Nothing to see here")
					// continue
				}
			}
		}
	}
}

func wsBinaryHandler(d *drbg.DRBG, refresh time.Duration, quantity int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(refresh * time.Millisecond)

		metrics.AddHTTPRequests(1)

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
			if v, verr := strconv.Atoi(q); verr == nil && v > 0 && v <= 1<<25 {
				quantity = v
			}
		}

		if x := r.URL.Query().Get("refresh"); x != "" {
			if z, zerr := strconv.Atoi(x); zerr == nil && z > 0 && z <= 60000 {
				dtime := time.Duration(z)
				refresh = dtime
				// log.Printf("Nothing to see here: %d", dtime)
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

		// io.Copy(buf, d)
		// buf := io.Reader(&d)
		buf := make([]byte, quantity)

		for {
			// buf := make([]byte, quantity)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = io.ReadFull(d, buf)
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				// d.Read(buf[:quantity])
				// d.Read(buf[quantity:])
				// d.Read(buf)

				err := conn.WriteMessage(websocket.BinaryMessage, buf)
				if err != nil {
					return
				}

				metrics.AddBytesGenerated(len(buf))
				metrics.AddWSPayloads(1)
				// rng.DecreaseActiveInstances(-1)
			default:
				{
					// d.Read(buf[:quantity])
					// d.Read(buf[quantity:])
					// io.ReadFull(&d, buf)
					// d.Read(buf)
					// log.Println("Nothing to see here")
					continue
				}
			}
		}
	}
}

func metricsHandler(d *drbg.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meta := d.GetMetadata()

		bytes := metrics.BytesGenerated() / 1024 / 1024
		reseeds := metrics.Reseeds()
		reqs := metrics.NumHTTPRequests()
		payloads := metrics.NumWSPayloads()
		entropy := metrics.BufferedBytes()
		entropyA := metrics.TestA()
		entropyB := metrics.TestB()

		// age := metrics.ReseedAgeMs
		age := d.ReseedAge().Milliseconds()

		bufBytes := meta.EntropyBufferedBytes / 1024
		bufCap := meta.EntropyFillPct
		DRBGcnt := meta.DRBGInstanceCnt

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

# HELP drbg_instance_count Total number of DBRG instances 
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

func healthHandler(d *drbg.DRBG) http.HandlerFunc {
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
		// w.WriteHeader(http.StatusOK)

		w.Header().Set("Content-Type", "application/json")

		err := json.NewEncoder(w).Encode(health)
		if err != nil {
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

// Kept for reference:
// func validateDevice(path string) error {
// 	info, err := os.Stat(path)
// 	if err != nil {
// 		return err
// 	}
//
// 	mode := info.Mode()
//
// 	if mode&os.ModeDevice == 0 {
// 		return fmt.Errorf("%s exists but is not a device file", path)
// 	}
//
// 	return nil
// }
//
// func testDeviceReadable(path string) error {
// 	f, err := os.Open(path)
// 	if err != nil {
// 		return fmt.Errorf("cannot open device %s: %w", path, err)
// 	}
// 	defer f.Close()
// 	return nil
// }

func validateEntropyDevice(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	// if mode&os.ModeCharDevice == 0
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

		log.Printf(Red+"Device %s unavailable, falling back to /dev/urandom"+Reset, path)

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

func startHTTP(ctx context.Context, addr string, handler http.Handler, master drbg.DRBG) (*http.Server, error) {
	// ln, err := net.Listen("tcp", addr)
	// if err != nil { return nil, err }
	// tln := newTunedListener(ln)
	ln, err := listener.NewTunedListener(ctx, addr, 4<<20)
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
			// seed, _ := master.Derive(32)
			// nonce, _ := master.Derive(12)
			// derive per-connection DRBG from master
			// childDRBG, _ := qrng.NewDRBG(seed)
			childDRBG, childerr := drbg.NewConnectionDRBG(master) // (DRBG)
			if childerr != nil {
				return ctx
			}
			// rng.DecreaseActiveInstances(-1)

			// attach to context for handlers
			return context.WithValue(cctx, connDRBGKey, childDRBG)
		},
	}

	// Serve loop
	go func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP serve error: %v", err)
		}
	}()

	// Context-driven graceful shutdown
	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			5*time.Second,
		)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}()

	return srv, nil
}

func startHTTPS(ctx context.Context, addr string, handler http.Handler, tlsConfig *tls.Config, master drbg.DRBG) (*http.Server, error) {
	ln, err := listener.NewTunedListener(ctx, addr, 4<<20)
	if err != nil {
		return nil, err
	}

	// cert, err := tls.LoadX509KeyPair(CertFile, KeyFile)
	// tlsConfig.Certificates = []tls.Certificate{cert}
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
			childDRBG, cerr := drbg.NewConnectionDRBG(master) // (DRBG)
			if cerr != nil {
				return ctx
			}
			// attach to context for handlers
			return context.WithValue(cctx, connDRBGKey, childDRBG)
		},
	}

	// enable HTTP/2
	/*
		http2.ConfigureServer(srv, &http2.Server{
			MaxConcurrentStreams: 1024,
			//InitialWindowSize:    1 << 20,
			//InitialConnWindowSize: 4 << 20,
			MaxReadFrameSize:     1 << 25,
		})
	*/

	// Serve loop
	go func() {
		err := srv.Serve(tlsLn)
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTPS serve error: %v", err)
		}
	}()

	// Context-driven shutdown
	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
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

	cfg := config.ParseConfig()

	if cfg.ReseedMs <= 0 || cfg.ReseedMs > 10000 {
		panic("reseed-ms must be between 1 and 10000 ms")
	}

	if cfg.MaxBytes > 33554432 {
		panic(Red + "max-bytes should be < 33554432 (32 MB)" + Reset)
	}

	if cfg.QRNGBuffer < 1 || cfg.QRNGBuffer > 4096 {
		panic("QRNG buffer size KB must be between 1 and 4096 KB")
	}

	fmt.Println("\n-------------------------------------------------")
	fmt.Println(Gray + "       Entropy Server Pre-Flight Checks" + Reset)
	fmt.Println("-------------------------------------------------")
	fmt.Println("HTTP port                    :", cfg.HTTPAddr)
	fmt.Println("HTTPS port                   :", cfg.HTTPSAddr)
	fmt.Println("CertFile TLS                 :", cfg.CertFile)
	fmt.Println("KeyFile TLS                  :", cfg.KeyFile)
	fmt.Println("EnableHTTPS flag             :", cfg.EnableHTTPS)
	fmt.Println("-------------------------------------------------")
	fmt.Println("Reseed size                  :", cfg.ReseedSize)
	fmt.Println("ReseedMs interval            :", cfg.ReseedMs)
	fmt.Println("Max request size KB          :", cfg.MaxBytes/1024)
	fmt.Println("QRNGBuffer size              :", cfg.QRNGBuffer)
	fmt.Println("Reseed Buffer size           :", cfg.SeedBuffer)
	fmt.Println("-------------------------------------------------")
	fmt.Println("Max number of Bytes          :", cfg.MaxBytes)
	fmt.Println("Maximum number of Words      :", cfg.MaxWords)
	fmt.Println("-------------------------------------------------")
	fmt.Printf("Refresh Rate (seconds)       : %ds\n", cfg.RefreshRate)
	fmt.Println("RefreshMs (ms)               :", cfg.RefreshRateMs)
	fmt.Println("RefreshColorMs (ms)          :", cfg.RefreshColorMs)
	fmt.Println("-------------------------------------------------")
	fmt.Println()

	// check for entropy source availability and access rights
	dev, err := ResolveEntropyDevice(cfg.DevicePath, cfg.RequireDevice)
	if err != nil {
		log.Fatal(err)
	}

	if derr := deviceExists(dev); derr != nil {
		log.Fatal(derr)
	}

	verr := validateEntropyDevice(dev)
	if verr != nil {
		log.Fatal("Entropy device validation failed: ", verr)
	}

	if dev == "/dev/urandom" {
		log.Println(Yellow + "Entropy mode: \"weak\" kernel CSPRNG fallback" + Reset)
	} else {
		log.Println(Green+"Entropy mode: robust RNG hardware device:", dev+Reset)
	}

	log.Printf("Entropy source: %s\n", dev)

	fmt.Println("	1) initialize entropy device as buffer source")

	qrngdev, _ := NewEntropySource(dev, cfg.RequireDevice)
	qrngBuf := qrng.NewQRNGBuffer(dev, cfg.QRNGBuffer) // 65536

	fmt.Println("	2) initialize seed space (needs fix)")

	/*
		seed, serr := fetchEntropy(64, dev, cfg.SeedBuffer) // 64*8 = 512 bits
		//seed, serr := fetchEntropy(64, dev, cfg.SeedBuffer) // 64*8 = 512 bits
		//seed, serr := fetchEntropy(cfg.SeedBuffer, dev, cfg.QRNGBuffer) // 64*8 = 512 bits
		if serr != nil {
			log.Fatal(serr)
		}
	*/

	g := make([]byte, 65536)
	entropy, _ := qrngdev.Read(g)
	// qrngdev.Read(g)

	fmt.Println("	3) initialize the entropy read pool (check size in config.go configuration file)")

	entropyPool = NewEntropyPool(entropy) // 8 MB pool
	// test write
	// entropyPool.Write(g)
	// entropyPool.Write(entropy.Read)

	fmt.Println("	4) start the entropy producer pool with given 64 KB of buffer")
	startEntropyProducer(entropyPool, dev, 65536)

	// resp := handler(pool)
	// assert(len(resp) == expected)

	// Initialize DRBG instance and pass entropy along. Note that multiple instances of DRBG are created on a per-connection basis
	fmt.Println("	5) write the entropy out to DRBG buffer")

	backup, derr := drbg.NewDRBG(entropyPool.buf)
	if derr != nil {
		log.Fatal(derr)
	}

	fmt.Println("	6) set DRBG metadata (optional)")
	// Set Metadata, for example PCI card "QRNG-idQuantique-QuantisPCI"
	backup.SetMetadata("1.1.0", dev, "ChaCha20", time.Duration(cfg.ReseedMs)*time.Millisecond, cfg.ReseedSize, *qrngBuf)

	// Attach the QRNG buffer for dynamic header reporting
	fmt.Println("	7) attach the QRNG buffer to DRBG")
	// Attach it to DRBG
	backup.SetEntropyBuffer(*qrngBuf)

	master, _ := drbg.NewDRBG(g)
	master.SetMetadata("1.1.0", dev, "ChaCha20", time.Duration(cfg.ReseedMs)*time.Millisecond, cfg.ReseedSize, *qrngBuf)
	// Attach it to DRBG
	master.SetEntropyBuffer(*qrngBuf)

	fmt.Println("	8) start network listener(s)")
	// tln := newTunedListener(ln)
	tlsCfg := transport.NewTLSConfig(cfg.CertFile, cfg.KeyFile)

	cert, crterr := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if crterr != nil {
		log.Fatal(crterr)
	}

	tlsCfg.Certificates = []tls.Certificate{cert}
	tlsCfg.ClientAuth = tls.NoClientCert

	/*
		fmt.Println("9) setup the masterDRBG")
		e := make([]byte, 65536)
		entropy2, _ := qrngdev.Read(e)
	*/

	// f := entropyPool.Read(e)
	// entropy := entropyPool.Read(2097152)
	// drbgmaster, _ := drbg.NewDRBG(entropy)
	// master := NewMasterDRBG(entropy)
	// masterDRBG, _ := drbg.NewDRBG(entropy2)

	// create the multiplexed listener proto
	mux := http.NewServeMux()

	// fs := http.FS(webFS)
	// http.Handle("/", http.FileServer(fs))
	// mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "./web/index.html")})

	mux.Handle("/", http.FileServer(http.Dir("./web")))

	mux.HandleFunc("/health-1", healthHandler(&backup))
	mux.HandleFunc("/health-2", healthHandler(&backup))

	mux.Handle("/metrics-1", metricsHandler(&master))
	mux.Handle("/metrics-2", metricsHandler(&backup))

	mux.HandleFunc("/stream", wsBinaryHandler(&master, cfg.RefreshColorMs, 2048))

	mux.HandleFunc("/colors", wsBytesHandler(&backup, cfg.RefreshColorMs))
	mux.HandleFunc("/bytes", wsBytesHandler(&backup, cfg.RefreshRateMs))
	mux.HandleFunc("/files", uploadHandler(&backup, cfg.BytesFingerprint, cfg.RefreshColorMs)) // cfg.MaxBytes
	// mux.HandleFunc("/files", fileAnalyzeHandler(drbg, cfg.BytesFingerprint, cfg.RefreshColorMs)) // cfg.MaxBytes

	mux.HandleFunc("/words", wsWordsHandler(cfg.MaxWords, cfg.RefreshColorMs))

	mux.HandleFunc("/v1/data/random", randomBytesHandler(&master, cfg.MaxBytes))
	mux.HandleFunc("/v1/data/test", randomHandler(&backup))
	mux.HandleFunc("/v1/image/random", randomImageHandler(&backup))
	mux.HandleFunc("/v1/image/heatmap", entropyHeatmapHandler(&backup))
	mux.HandleFunc("/v1/meta/random", entropyWordHandler(&backup, cfg.MaxWords, cfg.RefreshRate))
	mux.HandleFunc("/paroleparoleparole", entropyWordHandler(&backup, cfg.MaxWords, cfg.RefreshRate))

	// placeholder for QR-codes generation
	mux.HandleFunc("/v1/qr/random", qrHandler(&master))

	// placeholder for public/private key generation
	// mux.HandleFunc("/v1/cert/random", healthHandler(drbg))

	// context was here
	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()

	var (
		httpSrv  *http.Server
		httpsSrv *http.Server
		httpErr  error
		httpsErr error
	)

	// start HTTP & HTTPS servers on the same mux
	httpSrv, httpErr = startHTTP(ctx, cfg.HTTPAddr, mux, backup)
	if httpErr != nil {
		log.Fatal(httpErr)
	}

	// if os.Getenv("TLS") == "1"
	if cfg.EnableHTTPS {
		httpsSrv, httpsErr = startHTTPS(ctx, cfg.HTTPSAddr, mux, tlsCfg, backup)
		if httpsErr != nil {
			log.Fatal(httpsErr)
		}
	}

	fmt.Printf("	9) start HTTP server(s)\n")
	log.Println(Green+"HTTP server running on", cfg.HTTPAddr+Reset)

	if cfg.EnableHTTPS {
		log.Println(Green+"HTTPS server running on", cfg.HTTPSAddr+Reset)
	}

	if cfg.ShowDebug {
		go func() {
			muxdebug := http.NewServeMux()

			muxdebug.HandleFunc("/debug/pprof/", pprof.Index)
			muxdebug.HandleFunc("/debug/pprof/profile", pprof.Profile)
			muxdebug.HandleFunc("/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)
			muxdebug.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			muxdebug.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			muxdebug.HandleFunc("/debug/pprof/trace", pprof.Trace)

			_ = http.ListenAndServe("127.0.0.1:6060", muxdebug) //nolint:gosec // debug-only pprof endpoint, localhost
		}()

		log.Println("Debug server running on 127.0.0.1:6060/debug/pprof")
	}

	// Run permanent reseed loop
	fmt.Printf("	10) start reseed loop every %d ms\n", cfg.ReseedMs)

	// wait for first reseed to fully complete, especially useful when using fallback CSPRNG
	// time.Sleep(time.Duration(cfg.ReseedMs*2))

	// go reseedLoop(ctx, &master, cfg.ReseedMs, dev, cfg.QRNGBuffer, g)

	go func() {
		ticker := time.NewTicker(time.Duration(cfg.ReseedMs) * time.Millisecond)
		// ticker := time.NewTicker(time.Duration(cfg.ReseedMs))
		for range ticker.C {
			// entropy := entropyPool.Read(65536)
			go reseedLoop(ctx, &master, cfg.ReseedMs, dev, cfg.QRNGBuffer, g)
			// go reseedLoop(ctx, &backup, cfg.ReseedMs, dev, cfg.QRNGBuffer, g)
			// go reseedLoop(ctx, &master, cfg.ReseedMs, dev, cfg.QRNGBuffer, cfg.SeedBuffer)
			// go reseedLoop(ctx, &backup, cfg.ReseedMs, dev, cfg.QRNGBuffer, cfg.SeedBuffer)
			// go reseedLoop(ctx, &backup, cfg.ReseedMs, dev, cfg.SeedBuffer, cfg.SeedBuffer)
		}
	}()

	// go reseedLoop(ctx, &master, cfg.ReseedMs, dev, cfg.QRNGBuffer, cfg.SeedBuffer)
	// go reseedLoop(ctx, &backup, cfg.ReseedMs, dev, cfg.QRNGBuffer, cfg.SeedBuffer)

	log.Println(Gray + "Entropy Server startup sequence completed successfully\n" + Reset)

	<-ctx.Done()

	log.Println("shutdown signal received")

	// shutdownCtx, cancel := context.WithCancel(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Block and wait for shutdown
	if httpErr != nil {
		_ = httpSrv.Shutdown(shutdownCtx)
	}

	// if cfg.HTTPSAddr != ""
	if cfg.EnableHTTPS {
		if httpsErr != nil {
			_ = httpsSrv.Shutdown(shutdownCtx)
		}
	}

	log.Println("shutdown complete")
}

/*
func entropyJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	frame := generateEntropyFrame(8)

	json.NewEncoder(w).Encode(frame)
}

type EntropyFrame struct {
	Words string `json:"words"`
	Hash  string `json:"hash"`
}
*/

/*
func main() {

    cfg := config.Load()

    qrngDev := qrng.NewDevice(cfg.QRNGDevice)

    drbg := drbg.NewChaCha()

    svc := entropyservice.New(qrngDev, drbg)

    server := transport.NewHTTPServer(cfg.ListenAddr)

    server.Handle("/entropy", svc.FetchHandler)

    log.Fatal(server.Start())
}
*/
