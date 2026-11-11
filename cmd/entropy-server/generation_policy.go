package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

const (
	routeStream   = "/stream"
	routeColors   = "/colors"
	routeBytes    = "/bytes"
	routeWords    = "/words"
	routeRandom   = "/v1/data/random"
	routeTest     = "/v1/data/test"
	routeImage    = "/v1/image/random"
	routeHeatmap  = "/v1/image/heatmap"
	routeMeta     = "/v1/meta/random"
	routeWordPage = "/paroleparoleparole"
	routeQR       = "/v1/qr/random"
	routeSound    = "/sound"
)

// Keep every serving-root output route behind the same admission check. Upload
// analysis, independent lab bridges, probes, metrics and static UI do not draw
// from the serving root and remain available for diagnosis.
func registerGenerationRoutes(mux *http.ServeMux, root *drbg.DRBG, cfg *config.Config, ws *webSocketBudget, requests *httpRequestBudget) {
	routes := map[string]http.HandlerFunc{
		routeStream:   wsBinaryHandler(root, cfg.RefreshColorMs, 2048, cfg.MaxBytes, ws),
		routeColors:   wsBytesHandler(root, cfg.RefreshColorMs, cfg.MaxBytes, ws),
		routeBytes:    wsBytesHandler(root, cfg.RefreshRateMs, cfg.MaxBytes, ws),
		routeWords:    wsWordsHandler(root, cfg.MaxWords, cfg.RefreshColorMs, cfg.MaxBytes, cfg.WantsCookie, ws),
		routeRandom:   randomBytesHandler(root, cfg.MaxBytes, cfg.MaxWords, cfg.WantsCookie, requests),
		routeTest:     randomHandler(root, cfg.MaxBytes, requests),
		routeImage:    randomImageHandler(root, requests),
		routeHeatmap:  entropyHeatmapHandler(root, requests),
		routeMeta:     randomWordsHandler(root, cfg.MaxWords, cfg.PageRefreshMs, cfg.MaxBytes, cfg.WantsCookie, requests),
		routeWordPage: randomWordsHandler(root, cfg.MaxWords, cfg.PageRefreshMs, cfg.MaxBytes, cfg.WantsCookie, requests),
		routeQR:       requests.wrap(qrHandler(root), 4<<20),
		routeSound:    requests.wrap(randomSoundHandler(root), 8<<20),
	}
	for path, handler := range routes {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if err := root.CheckGeneration(); err != nil {
				writeGenerationUnavailable(w)
				return
			}

			handler(w, r)
		})
	}
}

func writeGenerationUnavailable(w http.ResponseWriter) {
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "1")
	http.Error(w, drbg.ErrSeedUnavailable.Error(), http.StatusServiceUnavailable)
}

func writeGenerationError(w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, drbg.ErrSeedUnavailable) {
		writeGenerationUnavailable(w)
		return
	}

	http.Error(w, fallback, http.StatusInternalServerError)
}

func closeUnavailableGeneration(conn *websocket.Conn, err error) {
	if errors.Is(err, drbg.ErrSeedUnavailable) {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Seed unavailable; reconnect after a successful reseed"),
			time.Now().Add(time.Second))
	}
}

// Watch both explicit failures and expiry even when the frame cadence is 60s or
// the source read hangs. Policy cancellation closes the socket to interrupt a
// blocked writer; cleanup joins both workers before releasing the child/budget.
func generationWebSocketContext(parent context.Context, conn *websocket.Conn, root *drbg.DRBG) (context.Context, func()) {
	policyContext, cancelPolicy := context.WithCancel(parent)
	ctx, cleanupSocket := websocketContext(policyContext, conn)
	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			changed, deadline, err := root.WatchGeneration()
			if err != nil {
				closeUnavailableGeneration(conn, err)
				cancelPolicy()

				return
			}

			var (
				timer   *time.Timer
				expires <-chan time.Time
			)

			if !deadline.IsZero() {
				timer = time.NewTimer(time.Until(deadline))
				expires = timer.C
			}

			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}

				return
			case <-changed:
			case <-expires:
			}

			if timer != nil {
				timer.Stop()
			}
		}
	}()

	return ctx, func() {
		cancelPolicy()
		cleanupSocket()
		<-done
	}
}
