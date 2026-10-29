package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

type wordTestSource struct{ reads atomic.Int64 }

func (s *wordTestSource) Read(p []byte) (int, error) {
	s.reads.Add(1)
	clear(p)

	return len(p), nil
}

func TestWordFrameSampling(t *testing.T) {
	// With 7,776 entries, masked 0xffff becomes 8,191 and must be rejected.
	// The following samples select indices 1, 0, 1, allowing repeated words.
	frame, err := newWordFrame(bytes.NewReader([]byte{0xff, 0xff, 0, 1, 0, 0, 0, 1}), 3)
	if err != nil || frame.Words != "abdomen abacus abdomen" {
		t.Fatalf("rejection sampling: frame=%+v err=%v", frame, err)
	}

	wantHash := sha256.Sum256([]byte(frame.Words))
	if frame.Hash != hex.EncodeToString(wantHash[:]) {
		t.Fatal("hash must identify the returned phrase")
	}

	for _, count := range []int{0, -1} {
		if _, err := newWordFrame(new(wordTestSource), count); err == nil {
			t.Errorf("accepted invalid count %d", count)
		}
	}

	partial, err := newWordFrame(bytes.NewReader([]byte{0}), 1)
	if !errors.Is(err, io.ErrUnexpectedEOF) || partial != (EntropyFrame{}) {
		t.Fatalf("short source must fail without returning a partial phrase: %+v, %v", partial, err)
	}
}

func TestWordPageCountAndCookie(t *testing.T) {
	wordPattern := regexp.MustCompile(`<div class="words">([^<]*)</div>`)
	hashPattern := regexp.MustCompile(`<div class="hash">([^<]*)</div>`)

	for _, tc := range []struct {
		name   string
		query  string
		cookie int
		want   int
	}{
		{"configured default", "", 0, 64},
		{"minimum", "?words=1", 0, 1},
		{"above old hash capacity", "?words=64", 0, 64},
		{"cookie", "", 7, 7},
		{"query overrides cookie", "?words=9", 7, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := new(wordTestSource)

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/meta/random"+tc.query, nil)
			if tc.cookie > 0 {
				prefs := httptest.NewRecorder()
				if err := setPrefsCookie(prefs, ClientPrefs{Bytes: 64, Words: tc.cookie, Refresh: 500}); err != nil {
					t.Fatal(err)
				}

				r.AddCookie(prefs.Result().Cookies()[0])
			}

			w := httptest.NewRecorder()

			randomWordsHandler(source, 64, 5000, 1<<25, tc.cookie > 0)(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}

			words := wordPattern.FindStringSubmatch(w.Body.String())

			hash := hashPattern.FindStringSubmatch(w.Body.String())
			if len(words) != 2 || len(hash) != 2 || len(strings.Fields(words[1])) != tc.want {
				t.Fatalf("wrong rendered word count/hash: %s", w.Body)
			}

			wantHash := sha256.Sum256([]byte(words[1]))
			if hash[1] != hex.EncodeToString(wantHash[:]) || source.reads.Load() == 0 {
				t.Fatal("page must use the supplied source and hash the phrase")
			}
		})
	}
}

func TestWordsRejectInvalidParameters(t *testing.T) {
	handler := wsWordsHandler(wordTestDRBG(t), 8, 500)

	for _, query := range []string{
		"words=0", "words=-1", "words=", "words=null", "words=abc", "words=9",
		"words=999999999999999999999", "words=1&words=2", "words=1&words=1",
		"refresh=0", "refresh=-1", "refresh=", "refresh=null", "refresh=abc",
		"refresh=60001", "refresh=999999999999999999999", "refresh=1&refresh=2",
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/words?"+query, nil)
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Sec-WebSocket-Version", "13")
		r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

		w := httptest.NewRecorder()
		handler(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", query, w.Code)
		}
	}
}

func TestWordsConcurrentClients(t *testing.T) {
	// A one-minute default makes receiving frames within three seconds proof
	// that each request's refresh is applied before ticker creation.
	server := httptest.NewServer(wsWordsHandler(wordTestDRBG(t), 8, 60000))
	defer server.Close()

	var wg sync.WaitGroup

	for count := 1; count <= 8; count++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			url := "ws" + strings.TrimPrefix(server.URL, "http") + "/words?words=" + strconv.Itoa(count) + "&refresh=1"

			conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), url, nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if err != nil {
				t.Error(err)

				return
			}

			defer conn.Close()

			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

			for range 3 {
				var frame EntropyFrame
				if err := conn.ReadJSON(&frame); err != nil {
					t.Error(err)

					return
				}

				if len(strings.Fields(frame.Words)) != count {
					t.Errorf("client wants %d words, got %q", count, frame.Words)
				}
			}
		}()
	}

	wg.Wait()

	// Explicit settings from earlier connections must not change defaults.
	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/words?refresh=1", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	var frame EntropyFrame
	if err := conn.ReadJSON(&frame); err != nil || len(strings.Fields(frame.Words)) != 8 {
		t.Fatalf("default changed across requests: frame=%+v err=%v", frame, err)
	}
}

func TestWordsIdleAndDisconnect(t *testing.T) {
	source := wordTestDRBG(t)
	reference := wordTestDRBG(t)
	done := make(chan struct{})
	handler := wsWordsHandler(source, 8, 60000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)

		handler(w, r)
	}))
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/words", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	time.Sleep(25 * time.Millisecond)

	got, err := source.Derive(64)
	if err != nil {
		t.Fatal(err)
	}

	want, err := reference.Derive(64)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("idle connection consumed parent output before its first tick")
	}

	_ = conn.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not stop the handler while its ticker was idle")
	}
}

func wordTestDRBG(t *testing.T) *drbg.DRBG {
	t.Helper()

	d, err := drbg.NewDRBG(make([]byte, 64))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { drbg.DecreaseActiveInstances(1) })

	return &d
}

func TestWordPageConvertsMillisecondRefresh(t *testing.T) {
	for _, tc := range []struct {
		milliseconds int
		seconds      string
	}{
		{1, "1"}, {999, "1"}, {1000, "1"}, {1500, "2"}, {5000, "5"},
	} {
		w := httptest.NewRecorder()
		randomWordsHandler(new(wordTestSource), 3, tc.milliseconds, 64, false)(w,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/meta/random", nil))

		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `http-equiv="refresh" content="`+tc.seconds+`"`) {
			t.Fatalf("%d milliseconds did not produce a %s-second HTML refresh", tc.milliseconds, tc.seconds)
		}
	}
}
