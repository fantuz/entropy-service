package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type uploadErrorReader struct{}

func (uploadErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type uploadZeroReader struct{}

func (uploadZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type shortUploadReader struct{ io.Reader }

func (r shortUploadReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(len(p), 7)]) }

func TestUploadStatisticsAndFullDigest(t *testing.T) {
	data := bytes.Repeat([]byte{0, 255}, 128)

	result, err := analyzeUpload(t.Context(), shortUploadReader{bytes.NewReader(data)}, 3)
	if err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(data)
	if result.TotalBytes != 256 || result.AnalyzedBytes != 3 || result.Histogram[0] != 2 || result.Histogram[255] != 1 ||
		result.Ones != 8 || result.Zeros != 16 || !bytes.Equal(result.Preview, []byte{0, 255, 0}) ||
		result.SHA256 != hex.EncodeToString(digest[:]) || result.Entropy < 0.91829 || result.Entropy > 0.91830 {
		t.Fatalf("incorrect partial-window statistics or full-file digest: %+v", result)
	}

	uniform := make([]byte, 256)
	for i := range uniform {
		uniform[i] = byte(i)
	}

	result, err = analyzeUpload(t.Context(), bytes.NewReader(uniform), 1024)
	if err != nil || result.Entropy != 8 || result.Ones != 1024 || result.Zeros != 1024 || result.AnalyzedBytes != 256 {
		t.Fatalf("uniform byte distribution: %+v, %v", result, err)
	}
}

func TestUploadHTTPValidationAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name, method, mediaType string
		body                    io.Reader
		declared                int64
		want                    int
	}{
		{"method", http.MethodGet, "", nil, 0, http.StatusMethodNotAllowed},
		{"missing content type", http.MethodPost, "", strings.NewReader("abc"), 3, http.StatusUnsupportedMediaType},
		{"multipart", http.MethodPost, "multipart/form-data; boundary=test", strings.NewReader("abc"), 3, http.StatusUnsupportedMediaType},
		{"empty", http.MethodPost, uploadMediaType, nil, 0, http.StatusBadRequest},
		{"declared oversized", http.MethodPost, uploadMediaType, strings.NewReader("x"), maxUploadBytes + 1, http.StatusRequestEntityTooLarge},
		{"chunked oversized", http.MethodPost, uploadMediaType, io.LimitReader(uploadZeroReader{}, maxUploadBytes+1), -1, http.StatusRequestEntityTooLarge},
		{"truncated", http.MethodPost, uploadMediaType, io.MultiReader(strings.NewReader("abc"), uploadErrorReader{}), -1, http.StatusBadRequest},
		{"maximum", http.MethodPost, uploadMediaType, io.LimitReader(uploadZeroReader{}, maxUploadBytes), -1, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := newHTTPRequestBudget(1, 1<<20)
			r := httptest.NewRequestWithContext(t.Context(), tc.method, "/files", tc.body)
			r.Header.Set("Content-Type", tc.mediaType)
			r.ContentLength = tc.declared
			w := httptest.NewRecorder()
			uploadHandler(4, budget)(w, r)

			if w.Code != tc.want || budget.usage().Active != 0 || budget.usage().Reserved != 0 {
				t.Fatalf("status %d, want %d; budget %+v", w.Code, tc.want, budget.usage())
			}

			if tc.want == http.StatusMethodNotAllowed && w.Header().Get("Allow") != "POST" {
				t.Fatal("method rejection must advertise POST")
			}

			if tc.want == http.StatusOK {
				var result fileAnalysis
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}

				if result.TotalBytes != maxUploadBytes || result.AnalyzedBytes != 4 || result.Zeros != 32 ||
					len(result.Preview) != 4 || len(result.SHA256) != 64 || w.Body.Len() > 8192 {
					t.Fatal("maximum upload produced unbounded or inaccurate output")
				}
			}
		})
	}
}

func TestUploadCancellationAndAdmission(t *testing.T) {
	budget := newHTTPRequestBudget(1, 1<<20)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/files", strings.NewReader("abc"))
	r.Header.Set("Content-Type", uploadMediaType)

	release, ok := budget.admit(httptest.NewRecorder(), r, uploadWorkingMemory)
	if !ok {
		t.Fatal("could not hold upload capacity")
	}
	defer release()

	w := httptest.NewRecorder()
	uploadHandler(4, budget)(w, r)

	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Fatal("upload bypassed HTTP admission")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	result, err := analyzeUpload(ctx, bytes.NewReader([]byte{1}), 4)
	if !errors.Is(err, context.Canceled) || result.TotalBytes != 0 {
		t.Fatal("canceled analysis returned data")
	}
}

func TestUploadShutdownInterruptsStalledBody(t *testing.T) {
	budget := newHTTPRequestBudget(1, 1<<20)
	server := httptest.NewUnstartedServer(countRequests(uploadHandler(4, budget)))
	managed := newManagedServer(t.Context(), server.Config)

	server.Start()
	defer server.Close()

	dialer := net.Dialer{Timeout: time.Second}

	conn, err := dialer.DialContext(t.Context(), "tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = io.WriteString(conn, "POST /files HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/octet-stream\r\nContent-Length: 65536\r\n\r\nx")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for budget.usage().Active != 1 {
		if time.Now().After(deadline) {
			t.Fatal("stalled upload was not admitted")
		}

		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if err := managed.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown did not interrupt the upload body: %v", err)
	}

	if budget.usage().Active != 0 || budget.usage().Reserved != 0 {
		t.Fatal("shutdown left upload capacity occupied")
	}

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))

	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("shutdown acknowledged an incomplete upload as successful")
	}
}

func TestOversizedChunkedUploadDoesNotWaitForMoreBody(t *testing.T) {
	budget := newHTTPRequestBudget(1, 1<<20)

	server := httptest.NewServer(countRequests(uploadHandler(4, budget)))
	defer server.Close()

	dialer := net.Dialer{Timeout: time.Second}

	conn, err := dialer.DialContext(t.Context(), "tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	_, err = fmt.Fprintf(conn, "POST /files HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/octet-stream\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n", maxUploadBytes+1)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := io.CopyN(conn, uploadZeroReader{}, maxUploadBytes+1); err != nil {
		t.Fatal(err)
	}

	// Deliberately omit the chunk terminator. A 413 must be sent without
	// trying to drain more of this already rejected request's body.
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("oversized upload blocked instead of receiving 413: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusRequestEntityTooLarge || !response.Close || budget.usage().Active != 0 {
		t.Fatalf("oversized upload was not closed cleanly: status=%d close=%t budget=%+v", response.StatusCode, response.Close, budget.usage())
	}
}
