package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"math/bits"
	"mime"
	"net/http"
	"time"
)

const (
	uploadMediaType           = "application/octet-stream"
	maxUploadBytes            = 32 << 20
	uploadPreviewBytes        = 2048
	uploadWorkingMemory int64 = 256 << 10
)

type fileAnalysis struct {
	TotalBytes    int64       `json:"total_bytes"`
	AnalyzedBytes int64       `json:"analyzed_bytes"`
	Histogram     [256]uint64 `json:"histogram"`
	Entropy       float64     `json:"entropy"`
	Ones          uint64      `json:"ones"`
	Zeros         uint64      `json:"zeros"`
	SHA256        string      `json:"sha256"`
	Preview       []byte      `json:"preview"`
}

func uploadHandler(analysisLimit int, budget *httpRequestBudget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "use POST with the raw file body", http.StatusMethodNotAllowed)

			return
		}

		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != uploadMediaType {
			http.Error(w, "Content-Type must be application/octet-stream", http.StatusUnsupportedMediaType)

			return
		}

		if r.ContentLength > maxUploadBytes {
			http.Error(w, "file exceeds 32 MiB upload limit", http.StatusRequestEntityTooLarge)

			return
		}

		release, admitted := budget.admit(w, r, uploadWorkingMemory)
		if !admitted {
			return
		}
		defer release()

		// A canceled request must also interrupt a stalled network body read.
		readInterrupted := make(chan struct{})

		stopInterrupt := context.AfterFunc(r.Context(), func() {
			defer close(readInterrupted)

			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
		})
		defer func() {
			if !stopInterrupt() {
				<-readInterrupted
			}
		}()

		// MaxBytesReader uses net/http's private connection-close hook on its
		// writer. Give it the original writer so logging cannot hide that hook.
		r.Body = http.MaxBytesReader(originalResponseWriter(w), r.Body, maxUploadBytes)

		result, err := analyzeUpload(r.Context(), r.Body, analysisLimit)
		if err != nil {
			if r.Context().Err() != nil {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "file analysis canceled", http.StatusServiceUnavailable)

				return
			}

			var sizeError *http.MaxBytesError
			if errors.As(err, &sizeError) {
				http.Error(w, "file exceeds 32 MiB upload limit", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "empty or incomplete file upload", http.StatusBadRequest)
			}

			return
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(result); err != nil {
			log.Printf("file analysis response write failed: %v", err)
		}
	}
}

func analyzeUpload(ctx context.Context, source io.Reader, analysisLimit int) (fileAnalysis, error) {
	var result fileAnalysis

	if analysisLimit < 1 || analysisLimit > maxUploadBytes {
		return result, errors.New("invalid file analysis limit")
	}

	digest := sha256.New()
	buffer := make([]byte, 32<<10)

	for {
		if err := ctx.Err(); err != nil {
			return fileAnalysis{}, err
		}

		n, err := source.Read(buffer)
		if n > 0 {
			result.TotalBytes += int64(n)
			_, _ = digest.Write(buffer[:n])

			analyzed := min(n, analysisLimit-int(result.AnalyzedBytes))
			for _, value := range buffer[:analyzed] {
				result.Histogram[value]++
				result.Ones += uint64(bits.OnesCount8(value))
			}

			result.AnalyzedBytes += int64(analyzed)
			preview := min(analyzed, uploadPreviewBytes-len(result.Preview))
			result.Preview = append(result.Preview, buffer[:preview]...)
		}

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return fileAnalysis{}, err
		}
	}

	if result.TotalBytes == 0 {
		return fileAnalysis{}, errors.New("empty file upload")
	}

	result.Zeros = uint64(result.AnalyzedBytes)*8 - result.Ones

	result.SHA256 = hex.EncodeToString(digest.Sum(nil))
	for _, count := range result.Histogram {
		if count > 0 {
			p := float64(count) / float64(result.AnalyzedBytes)
			result.Entropy -= p * math.Log2(p)
		}
	}

	return result, nil
}
