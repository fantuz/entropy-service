package main

import (
	"context"
	"crypto/tls"
	"embed"
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
	"io/fs"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/listener"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/rdrand"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/transport"
)

//go:embed web
var webFS embed.FS

// GoReleaser injects the release tag; development binaries report dev.
var appVersion = "dev"

type ClientPrefs struct {
	Bytes   int `json:"bytes"`
	Refresh int `json:"refresh"`
	Words   int `json:"words"`
}

type EntropySource interface {
	Read(p []byte) (int, error)
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

// var qrngBuffer QRNGBuffer

// var entropyPool EntropyPool

/*
var bufPool = sync.Pool{
	New: func() any {
		return make([]byte, 1<<25) // 32 MB
	},
}
*/

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
		MaxAge:   86400, // 1 day
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

/*
func extractPrefs(r *http.Request) ClientPrefs {
	q := r.URL.Query()

	prefs := ClientPrefs{
		Bytes:   524288,
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

	log.Printf("Cookie Bytes: %v\n", prefs.Bytes)
	log.Printf("Cookie Refresh: %v\n", prefs.Refresh)
	log.Printf("Cookie Words: %v\n", prefs.Words)

	return prefs
}
*/

func extractByteCount(r *http.Request, defaultBytes, maxBytes int) (int, error) {
	values, present := r.URL.Query()["bytes"]
	if !present {
		return min(defaultBytes, maxBytes), nil
	}

	if len(values) != 1 {
		return 0, errors.New("bytes must be supplied once")
	}

	n, err := strconv.Atoi(values[0])
	if err != nil || n <= 0 || n > maxBytes {
		return 0, fmt.Errorf("bytes must be between 1 and %d", maxBytes)
	}

	return n, nil
}

func extractPrefs(r *http.Request, maxBytes, maxWords int, hasCookie bool) (ClientPrefs, error) {
	prefs := ClientPrefs{
		Bytes:   min(2097152, maxBytes),
		Refresh: 500,
		Words:   maxWords,
	}

	if hasCookie {
		prefs.Bytes = min(524288, maxBytes)
		prefs.Words = min(3, maxWords)

		if c, err := getPrefsCookie(r); err == nil {
			if c.Bytes > 0 && c.Bytes <= maxBytes {
				prefs.Bytes = c.Bytes
			}

			if c.Words > 0 && c.Words <= maxWords {
				prefs.Words = c.Words
			}

			if c.Refresh > 0 {
				prefs.Refresh = c.Refresh
			}
		}
	}

	var err error

	prefs.Bytes, err = extractByteCount(r, prefs.Bytes, maxBytes)
	if err != nil {
		return ClientPrefs{}, err
	}

	q := r.URL.Query()
	fields := []struct {
		name string
		dst  *int
		max  int
	}{
		{"words", &prefs.Words, maxWords},
		{"refresh", &prefs.Refresh, 0},
	}

	for _, field := range fields {
		values, present := q[field.name]
		if !present {
			continue
		}

		if len(values) != 1 {
			return ClientPrefs{}, fmt.Errorf("%s must be supplied once", field.name)
		}

		v, err := strconv.Atoi(values[0])
		if err != nil || v <= 0 || (field.max > 0 && v > field.max) {
			return ClientPrefs{}, fmt.Errorf("invalid %s value", field.name)
		}

		*field.dst = v
	}

	return prefs, nil
}

// Gorilla's default origin policy rejects cross-origin browser requests.
// Non-browser clients with no Origin header remain supported.
var upgrader = websocket.Upgrader{}

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

// Active reseed loop: reads from the configured entropy source and updates
// the serving generator. Former QRNGBuffer and reseed prototypes were unused.
func reseedLoop(ctx context.Context, source io.Reader, interval, size int, generators ...*drbg.DRBG) {
	// An internal caller must not bypass the CLI's size guard and allocate a
	// negative/huge buffer or falsely claim freshness with an empty seed.
	if size < 32 || size > 1<<20 {
		err := fmt.Errorf("invalid reseed buffer size %d bytes (expected 32..1048576)", size)
		for _, d := range generators {
			d.RecordReseedFailure(err)
		}

		return
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()

	seed := make([]byte, size)
	defer clear(seed)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i, d := range generators {
				if ctx.Err() != nil {
					return
				}

				if _, err := io.ReadFull(source, seed); err != nil {
					clear(seed)

					if ctx.Err() != nil {
						return
					}

					log.Printf("reseed entropy read failed: %v", err)

					for _, pending := range generators[i:] {
						pending.RecordReseedFailure(err)
					}

					break
				}

				if ctx.Err() != nil {
					return
				}

				reseedErr := d.Reseed(seed)
				clear(seed)

				if reseedErr != nil {
					log.Printf("reseed failed: %v", reseedErr)
					d.RecordReseedFailure(reseedErr)

					continue
				}

				metrics.AddReseeds(1)
			}
		}
	}
}

func entropyHeatmapHandler(d *drbg.DRBG) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		width := 1024
		height := 1024

		img := image.NewRGBA(image.Rect(0, 0, width, height))

		buf := make([]byte, width*height)
		if _, err := io.ReadFull(d, buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

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
		w.Header().Set("X-Rng-Reseed-Age-Ms",
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
		if _, err := io.ReadFull(d, buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

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
		if _, err := io.ReadFull(d, img.Pix); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

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
		w.Header().Set("X-Rng-Reseed-Age-Ms-Test",
			strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))
		_ = png.Encode(w, img)
	}
}

func randomHandler(d *drbg.DRBG, maxBytes int, budget *httpRequestBudget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, err := extractByteCount(r, 1024, maxBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		release, ok := budget.admit(w, r, httpReservation(n, 1))
		if !ok {
			return
		}
		defer release()

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Entropy-Metric", "random-data")
		w.Header().Set("X-Rng-Reseed-Age-Ms-Test",
			strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

		if err := streamEntropyResponse(w, r, d, n); err != nil {
			log.Printf("test entropy stream interrupted: %v", err)
		}
	}
}

func wsWordsHandler(d *drbg.DRBG, maxWords int, defaultRefresh int, budget *webSocketBudget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		quantity, refresh := maxWords, defaultRefresh
		q := r.URL.Query()
		fields := []struct {
			name string
			dst  *int
			max  int
		}{
			{"words", &quantity, maxWords},
			{"refresh", &refresh, 60000},
		}

		for _, field := range fields {
			values, present := q[field.name]
			if !present {
				continue
			}

			if len(values) != 1 {
				http.Error(w, field.name+" must be supplied once", http.StatusBadRequest)

				return
			}

			v, err := strconv.Atoi(values[0])
			if err != nil || v <= 0 || v > field.max {
				http.Error(w, "invalid "+field.name+" value", http.StatusBadRequest)

				return
			}

			*field.dst = v
		}

		release, admitted := budget.admit(w, r, wsReservation(quantity, budget.wordMemory))
		if !admitted {
			return
		}
		defer release()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed (wsWordsHandler): %v", err)

			return
		}
		defer conn.Close()

		source := drbg.NewConnectionReader(d)
		defer source.Close()

		ctx, cancel := websocketContext(r.Context(), conn)
		defer cancel()

		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				frame, err := newWordFrame(source, quantity)
				if err != nil {
					log.Printf("ws word generation failed: %v", err)

					return
				}

				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteJSON(frame); err != nil {
					return
				}

				metrics.AddWSPayloads(1)
			}
		}
	}
}

func randomWordsHandler(source io.Reader, quantity int, refreshMs int, maxBytes int, hasCookie bool, budget *httpRequestBudget) http.HandlerFunc {
	page := template.Must(template.New("page").Parse(`
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
`))

	return func(w http.ResponseWriter, r *http.Request) {
		prefs, err := extractPrefs(r, maxBytes, quantity, hasCookie)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		release, ok := budget.admit(w, r, httpReservation(prefs.Words, budget.wordMemory))
		if !ok {
			return
		}
		defer release()

		if hasCookie {
			_ = setPrefsCookie(w, prefs)
		}

		frame, err := newWordFrame(source, prefs.Words)
		if err != nil {
			http.Error(w, "word generation failed", http.StatusInternalServerError)

			return
		}

		// HTML meta refresh uses integer seconds, so round milliseconds up.
		refreshSeconds := refreshMs / 1000
		if refreshMs%1000 != 0 {
			refreshSeconds++
		}

		data := struct {
			Words   string
			Hash    string
			Refresh int
		}{
			Words:   frame.Words,
			Hash:    frame.Hash,
			Refresh: refreshSeconds,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		if err := page.Execute(w, data); err != nil {
			log.Printf("word page write failed: %v", err)

			return
		}
	}
}

// func randomBytesHandler(d *drbg.DRBG, _ int, hasCookie bool) http.HandlerFunc
func randomBytesHandler(d *drbg.DRBG, maxBytes, maxWords int, hasCookie bool, budget *httpRequestBudget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// max 2MB
		// size := 2097152
		prefs, err := extractPrefs(r, maxBytes, maxWords, hasCookie)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		release, ok := budget.admit(w, r, httpReservation(prefs.Bytes, 1))
		if !ok {
			return
		}
		defer release()

		if hasCookie {
			// prefs := extractPrefs(r)
			_ = setPrefsCookie(w, prefs)
			// size = prefs.Bytes
		}

		size := prefs.Bytes

		d.WriteHeaders(w)
		w.Header().Set("Content-Type", "application/octet-stream")

		// Create a per-request DRBG using the parent's selected backend.
		child, err := drbg.NewConnectionDRBG(d)
		if err != nil {
			http.Error(w, "DRBG child creation failed", http.StatusInternalServerError)

			return
		}
		defer func() { _ = child.Close() }()
		// child, _ := qrng.Context().Value("conn_drbg").(*qrng.NewDRBG)

		/*
			if q := r.URL.Query().Get("bytes"); q != "" {
				if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 1<<25 {
					size = v
				}
			}
		*/

		// Stream from this request's private DRBG, checking cancellation each chunk.
		if err := streamEntropyResponse(w, r, &child, size); err != nil {
			// A response may already have started: never append an HTTP error body.
			log.Printf("random bytes stream interrupted: %v", err)
		}
	}
}

func wsBytesHandler(d *drbg.DRBG, defaultRefresh int, maxBytes int, budget *webSocketBudget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format, err := parseByteFrameFormat(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		n, err := extractByteCount(r, 2048, maxBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		refresh := defaultRefresh

		if q := r.URL.Query().Get("refresh"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 60000 {
				refresh = v
			}
		}

		release, admitted := budget.admit(w, r, wsByteReservation(n, format == byteFrameJSON))
		if !admitted {
			return
		}
		defer release()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed (wsBytesHandler): %v", err)

			return
		}
		defer conn.Close()

		source := drbg.NewConnectionReader(d)
		defer source.Close()

		ctx, cancel := websocketContext(r.Context(), conn)
		defer cancel()

		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		defer ticker.Stop()

		buf := make([]byte, n)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := io.ReadFull(source, buf); err != nil {
					log.Printf("ws entropy read failed: %v", err)

					return
				}

				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

				if err := writeByteFrame(conn, buf, format); err != nil {
					return
				}

				metrics.AddWSPayloads(1)
			}
		}
	}
}

/*
// replaced october 2026
func wsBytesHandler(d *drbg.DRBG, refresh time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(refresh * time.Millisecond)

		metrics.AddHTTPRequests(1)

		conn, cerr := upgrader.Upgrade(w, r, nil)
		if cerr != nil {
			log.Println("ws upgrade failed (wsBytesHandler)")
			log.Println(cerr)

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
					Hex:    hex.EncodeToString(buf[:n]),
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
			default:
				{
					// log.Println("Nothing to see here")
					// continue
				}
			}
		}
	}
}
*/

func wsBinaryHandler(d *drbg.DRBG, defaultRefresh int, defaultQuantity, maxBytes int, budget *webSocketBudget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		quantity, err := extractByteCount(r, defaultQuantity, maxBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		refresh := defaultRefresh

		if q := r.URL.Query().Get("refresh"); q != "" {
			if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 60000 {
				refresh = v
			}
		}

		release, admitted := budget.admit(w, r, wsByteReservation(quantity, false))
		if !admitted {
			return
		}
		defer release()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed (wsBinaryHandler): %v", err)

			return
		}
		defer conn.Close()

		source := drbg.NewConnectionReader(d)
		defer source.Close()

		ctx, cancel := websocketContext(r.Context(), conn)
		defer cancel()

		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		defer ticker.Stop()

		buf := make([]byte, quantity)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := io.ReadFull(source, buf); err != nil {
					log.Printf("ws entropy read failed: %v", err)

					return
				}

				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

				if err := conn.WriteMessage(websocket.BinaryMessage, buf); err != nil {
					return
				}

				metrics.AddWSPayloads(1)
			}
		}
	}
}

/*
// replaced october 2026
func wsBinaryHandler(d *drbg.DRBG, refresh time.Duration, quantity int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ticker := time.NewTicker(refresh * time.Millisecond)

		metrics.AddHTTPRequests(1)

		// ua := r.Header.Get("Connection")
		// log.Println(ua)
		// w.Header().Del("Connection")
		// w.Header().Set("Connection", "Upgrade")
		// w.Header().Set("Upgrade", "websocket")
		// log.Println(ua)

		conn, cerr := upgrader.Upgrade(w, r, nil)
		if cerr != nil {
			log.Println("ws upgrade failed (wsBinaryHandler)")
			log.Println(cerr)

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
*/

// func startHTTP(ctx context.Context, addr string, handler http.Handler, master drbg.DRBG) (*http.Server, error)
// func startHTTP(ctx context.Context, addr string, handler http.Handler, master *drbg.DRBG) (*http.Server, error)
func startHTTP(ctx context.Context, addr string, handler http.Handler) (*managedServer, error) {
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
		/*
			ConnContext: func(cctx context.Context, c net.Conn) context.Context {
				// seed, _ := master.Derive(32)
				// nonce, _ := master.Derive(12)
				// derive per-connection DRBG from master
				// childDRBG, _ := qrng.NewDRBG(seed)
				childDRBG, childerr := drbg.NewConnectionDRBG(master) // (DRBG)
				if childerr != nil {
					return ctx
				}

				// attach to context for handlers
				// return context.WithValue(cctx, connDRBGKey, childDRBG)
				return context.WithValue(cctx, connDRBGKey, &childDRBG)
			},
		*/
	}

	managed := newManagedServer(ctx, srv)

	// Serve loop
	go func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP serve error: %v", err)
		}
	}()

	return managed, nil
}

// func startHTTPS(ctx context.Context, addr string, handler http.Handler, tlsConfig *tls.Config, master drbg.DRBG) (*http.Server, error)
// func startHTTPS(ctx context.Context, addr string, handler http.Handler, tlsConfig *tls.Config, master *drbg.DRBG) (*http.Server, error)
func startHTTPS(ctx context.Context, addr string, handler http.Handler, tlsConfig *tls.Config) (*managedServer, error) {
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
		/*
			ConnContext: func(cctx context.Context, c net.Conn) context.Context {
				childDRBG, cerr := drbg.NewConnectionDRBG(master)
				if cerr != nil {
					return ctx
				}
				// attach to context for handlers
				// return context.WithValue(cctx, connDRBGKey, childDRBG)
				return context.WithValue(cctx, connDRBGKey, &childDRBG)
			},
		*/
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

	managed := newManagedServer(ctx, srv)

	// Serve loop
	go func() {
		err := srv.Serve(tlsLn)
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTPS serve error: %v", err)
		}
	}()

	return managed, nil
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

	algorithm, err := drbg.ParseAlgorithm(cfg.DRBG)
	if err != nil {
		log.Fatal(err)
	}

	maxReseedAge := time.Duration(cfg.MaxReseedAgeMs) * time.Millisecond

	fmt.Println("\n-------------------------------------------------")
	fmt.Println(Cyan + "       Entropy Server Pre-Flight Checks" + Reset)
	fmt.Println("-------------------------------------------------")
	fmt.Println("HTTP cookie support          :", cfg.WantsCookie)
	fmt.Println("HTTP port                    :", cfg.HTTPAddr)
	fmt.Println("HTTPS port                   :", cfg.HTTPSAddr)
	fmt.Println("CertFile TLS                 :", cfg.CertFile)
	fmt.Println("KeyFile TLS                  :", cfg.KeyFile)
	fmt.Println("EnableHTTPS flag             :", cfg.EnableHTTPS)
	fmt.Println("-------------------------------------------------")
	fmt.Println("Reseed size                  :", cfg.ReseedSize)
	fmt.Println("ReseedMs interval            :", cfg.ReseedMs)
	fmt.Println("Max request size KB          :", cfg.MaxBytes/1024)
	fmt.Println("DRBG stream backend          :", algorithm)
	fmt.Println("Readiness max seed age (ms)  :", cfg.MaxReseedAgeMs)
	fmt.Println("Legacy entropy buffer       : ignored (-buffer-entropy)")
	fmt.Println("Legacy QRNG buffer          : ignored (-buffer-qrng)")
	fmt.Println("-------------------------------------------------")
	fmt.Println("Max number of Bytes          :", cfg.MaxBytes)
	fmt.Println("Maximum number of Words      :", cfg.MaxWords)
	fmt.Println("-------------------------------------------------")
	fmt.Printf("Page refresh (ms)           : %d\n", cfg.PageRefreshMs)
	fmt.Println("RefreshMs (ms)               :", cfg.RefreshRateMs)
	fmt.Println("RefreshColorMs (ms)          :", cfg.RefreshColorMs)
	fmt.Println("-------------------------------------------------")
	fmt.Println()

	// Select and open exactly one source. Report the opened reader's identity,
	// not merely the path requested before potential fallback.
	qrngdev, actualSource, err := openEntropySource(cfg.DevicePath, cfg.RequireDevice)
	if err != nil {
		log.Fatalf("open entropy source: %v", err)
	}
	defer qrngdev.Close()

	var seedReader io.Reader = qrngdev

	if actualSource == DefaultCSPRNG || actualSource == sourceGoCryptoRand {
		log.Printf("Entropy mode: operating-system CSPRNG (%s)", actualSource)
	} else {
		log.Printf("Entropy mode: configured character device (%s); quality not independently verified", actualSource)
	}

	log.Printf("Entropy source: %s", actualSource)

	fmt.Println("\t1) open entropy seed source")

	if cfg.MixCPU {
		seedReader = rdrand.NewCPUMixer(seedReader, func(err error) {
			log.Printf("CPU seed mixing unavailable: %v; continuing with configured entropy source", err)
		})

		log.Printf("Seed source: configured entropy source with optional CPU mixing (supported RDSEED=%t, RDRAND=%t)", rdrand.RDSEEDAvailable(), rdrand.Available())
	} else if cfg.MixRDRAND {
		mixed, err := rdrand.NewMixer(seedReader, func(err error) {
			log.Printf("RDRAND mixing unavailable: %v; continuing with configured entropy source", err)
		})
		if err != nil {
			log.Fatalf("enable RDRAND mixing: %v", err)
		}

		seedReader = mixed

		log.Print("Seed source: configured entropy source with optional RDRAND mixing")
	}

	fmt.Println("	2) read root DRBG seed")

	seed := make([]byte, 64)
	if _, err := io.ReadFull(seedReader, seed); err != nil {
		log.Fatalf("read root seed: %v", err)
	}

	fmt.Println("	3) initialize root DRBG")

	root, err := drbg.NewDRBGWithAlgorithm(seed, algorithm)
	clear(seed)

	if err != nil {
		log.Fatal(err)
	}

	defer root.Close()

	// Set Metadata, for example PCI card "QRNG-idQuantique-QuantisPCI".
	fmt.Println("	4) set DRBG metadata (optional)")
	root.SetMetadata(appVersion, actualSource, algorithm.String(), time.Duration(cfg.ReseedMs)*time.Millisecond, cfg.ReseedSize*8)

	var tlsCfg *tls.Config

	/*
		tlsCfg := transport.NewTLSConfig(cfg.CertFile, cfg.KeyFile)
		cert, crterr := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if crterr != nil {
			log.Fatal(crterr)
		}

		tlsCfg.Certificates = []tls.Certificate{cert}
		tlsCfg.ClientAuth = tls.NoClientCert
	*/

	if cfg.EnableHTTPS {
		tlsCfg = transport.NewTLSConfig(cfg.CertFile, cfg.KeyFile)
	}

	wsBudget := newWebSocketBudget(cfg.WSMaxConnections, cfg.WSMemoryMiB*(1<<20))
	log.Printf("WebSocket limits: %d connections, %d MiB estimated working memory, %d-byte inbound messages", cfg.WSMaxConnections, cfg.WSMemoryMiB, wsInboundLimit)
	httpBudget := newHTTPRequestBudget(cfg.HTTPMaxRequests, cfg.HTTPMemoryMiB*(1<<20))
	activityMonitor := startActivityPeakMonitor(ctx, wsBudget, httpBudget)

	log.Printf("HTTP generation limits: %d requests, %d MiB estimated working memory", cfg.HTTPMaxRequests, cfg.HTTPMemoryMiB)

	// create the multiplexed listener proto
	mux := http.NewServeMux()

	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("open embedded web assets: %v", err)
	}

	mux.Handle("/", http.FileServer(http.FS(webRoot)))

	mux.HandleFunc("/live", livenessHandler)
	mux.HandleFunc("/ready", healthHandler(&root, maxReseedAge))

	// Keep numbered health and metrics routes as aliases of the same root.
	mux.HandleFunc("/health-1", healthHandler(&root, maxReseedAge))
	mux.HandleFunc("/health-2", healthHandler(&root, maxReseedAge))

	mux.Handle("/metrics-1", metricsHandler(&root, maxReseedAge, wsBudget, httpBudget, activityMonitor))
	mux.Handle("/metrics-2", metricsHandler(&root, maxReseedAge, wsBudget, httpBudget, activityMonitor))

	mux.HandleFunc("/stream", wsBinaryHandler(&root, cfg.RefreshColorMs, 2048, cfg.MaxBytes, wsBudget))
	mux.HandleFunc("/colors", wsBytesHandler(&root, cfg.RefreshColorMs, cfg.MaxBytes, wsBudget))
	mux.HandleFunc("/bytes", wsBytesHandler(&root, cfg.RefreshRateMs, cfg.MaxBytes, wsBudget))
	mux.HandleFunc("/files", uploadHandler(cfg.BytesFingerprint, httpBudget))

	mux.HandleFunc("/words", wsWordsHandler(&root, cfg.MaxWords, cfg.RefreshColorMs, wsBudget))

	// mux.HandleFunc("/v1/data/random", randomBytesHandler(&root, cfg.MaxBytes, cfg.WantsCookie))
	mux.HandleFunc("/v1/data/random", randomBytesHandler(&root, cfg.MaxBytes, cfg.MaxWords, cfg.WantsCookie, httpBudget))
	mux.HandleFunc("/v1/data/test", randomHandler(&root, cfg.MaxBytes, httpBudget))
	// Fixed 1024x1024 images: RGBA pixels, source bytes and PNG scratch.
	mux.HandleFunc("/v1/image/random", httpBudget.wrap(randomImageHandler(&root), 16<<20))
	mux.HandleFunc("/v1/image/heatmap", httpBudget.wrap(entropyHeatmapHandler(&root), 16<<20))
	// mux.HandleFunc("/v1/meta/random", randomWordsHandler(&root, cfg.MaxWords, cfg.PageRefreshMs, cfg.WantsCookie))
	mux.HandleFunc("/v1/meta/random", randomWordsHandler(&root, cfg.MaxWords, cfg.PageRefreshMs, cfg.MaxBytes, cfg.WantsCookie, httpBudget))
	mux.HandleFunc("/v1/qr/random", httpBudget.wrap(qrHandler(&root), 4<<20))
	// Up to five seconds of samples plus WAV output; keep the sound slot limit.
	mux.HandleFunc("/sound", httpBudget.wrap(randomSoundHandler(&root), 8<<20))

	// mux.HandleFunc("/paroleparoleparole", randomWordsHandler(&root, cfg.MaxWords, cfg.PageRefreshMs, cfg.WantsCookie))
	mux.HandleFunc("/paroleparoleparole", randomWordsHandler(&root, cfg.MaxWords, cfg.PageRefreshMs, cfg.MaxBytes, cfg.WantsCookie, httpBudget))

	// placeholder for public/private key generation
	// mux.HandleFunc("/v1/cert/random", healthHandler(drbg))

	// context was here
	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()
	// start HTTP & HTTPS servers on the same mux
	// os.Getenv("TLS") == "1"

	fmt.Printf("	5) start HTTP server(s)\n")

	var (
		httpSrv  *managedServer
		httpsSrv *managedServer
		debugSrv *managedServer
		httpErr  error
		httpsErr error
	)

	httpSrv, httpErr = startHTTP(ctx, cfg.HTTPAddr, countRequests(mux))
	if httpErr != nil {
		log.Fatal(httpErr)
	} else {
		log.Println(Green+"HTTP server running on", cfg.HTTPAddr+Reset)
	}

	if cfg.EnableHTTPS {
		httpsSrv, httpsErr = startHTTPS(ctx, cfg.HTTPSAddr, countRequests(mux), tlsCfg)
		if httpsErr != nil {
			log.Fatal(httpsErr)
		} else {
			log.Println(Green+"HTTPS server running on", cfg.HTTPSAddr+Reset)
		}
	}

	if cfg.ShowDebug {
		muxdebug := http.NewServeMux()
		muxdebug.HandleFunc("/debug/pprof/", pprof.Index)
		muxdebug.HandleFunc("/debug/pprof/profile", pprof.Profile)
		muxdebug.HandleFunc("/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)
		muxdebug.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		muxdebug.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		muxdebug.HandleFunc("/debug/pprof/trace", pprof.Trace)

		debugSrv = newManagedServer(ctx, &http.Server{
			Addr: "127.0.0.1:6060", Handler: muxdebug, ReadHeaderTimeout: 5 * time.Second,
		})

		go func() {
			if err := debugSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("debug serve error: %v", err)
			}
		}()

		log.Println("Debug server running on 127.0.0.1:6060/debug/pprof")
	}

	// Run permanent reseed loop
	fmt.Printf("	6) start reseed loop every %d ms\n", cfg.ReseedMs)

	go reseedLoop(ctx, seedReader, cfg.ReseedMs, cfg.ReseedSize, &root)

	log.Println(Cyan + "Entropy Server startup sequence completed successfully\n" + Reset)

	<-ctx.Done()

	log.Println(Red + "shutdown signal received" + Reset)

	// shutdownCtx, cancel := context.WithCancel(context.Background())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop listeners together and join all handlers, including WebSockets.
	var shutdown sync.WaitGroup

	for _, srv := range []*managedServer{httpSrv, httpsSrv, debugSrv} {
		if srv == nil {
			continue
		}

		shutdown.Add(1)

		go func() {
			defer shutdown.Done()

			if err := srv.Shutdown(shutdownCtx); err != nil {
				log.Printf("shutdown %s: %v", srv.Addr, err)
			}
		}()
	}

	shutdown.Wait()

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
