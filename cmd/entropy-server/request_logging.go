package main

import (
	"bufio"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

type statusWriter struct {
	http.ResponseWriter

	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func originalResponseWriter(w http.ResponseWriter) http.ResponseWriter {
	for {
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return w
		}

		w = wrapper.Unwrap()
	}
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}

	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(code)

		return
	}

	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Flush() {
	_ = w.FlushError()
}

func (w *statusWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err == nil && w.status == 0 {
		w.status = http.StatusOK
	}

	return err
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil && w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}

	return conn, buffer, err
}

func (w *statusWriter) ReadFrom(source io.Reader) (int64, error) {
	if reader, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := reader.ReadFrom(source)
		if n > 0 && w.status == 0 {
			w.status = http.StatusOK
		}

		return n, err
	}

	return io.Copy(struct{ io.Writer }{w}, source)
}

func (w *statusWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}

	return http.ErrNotSupported
}

func observeRequests(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.AddHTTPRequests(1)

		started := time.Now()

		response := &statusWriter{ResponseWriter: w}
		defer func() {
			if response.status >= http.StatusBadRequest {
				logger.Printf("HTTP request failed method=%q path=%q status=%d duration_ms=%.3f remote=%q",
					r.Method, r.URL.Path, response.status, time.Since(started).Seconds()*1000, r.RemoteAddr)
			}
		}()

		next.ServeHTTP(response, r)
	})
}
