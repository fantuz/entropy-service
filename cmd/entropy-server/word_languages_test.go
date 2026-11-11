package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/wordlist"
)

func TestWordLanguagesSamplingAndExactUnicodeHash(t *testing.T) {
	for _, info := range wordlist.Catalog() {
		t.Run(info.Code, func(t *testing.T) {
			dictionary, _ := wordlist.Lookup(info.Code)

			var draws [6]byte
			binary.BigEndian.PutUint16(draws[2:4], uint16(dictionary.Size()-1))
			frame, err := newWordFrameFromList(bytes.NewReader(draws[:]), 3, dictionary)
			want := dictionary.Word(0) + " " + dictionary.Word(dictionary.Size()-1) + " " + dictionary.Word(0)

			digest := sha256.Sum256([]byte(want))
			if err != nil || frame.Words != want || frame.Hash != hex.EncodeToString(digest[:]) || frame.Language != info.Code || frame.Wordlist != info.ID {
				t.Fatalf("wrong language/UTF-8 sampling: %+v %v", frame, err)
			}
		})
	}
}

func TestWordLanguagesHTTPPages(t *testing.T) {
	for _, info := range wordlist.Catalog() {
		t.Run(info.Code, func(t *testing.T) {
			dictionary, _ := wordlist.Lookup(info.Code)
			source := new(wordTestSource)
			w := httptest.NewRecorder()
			randomWordsHandler(source, 4, 500, 64, false, newHTTPRequestBudget(8, 1<<20))(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/meta/random?words=2&lang="+info.Code, nil))

			want := dictionary.Word(0) + " " + dictionary.Word(0)
			digest := sha256.Sum256([]byte(want))

			if w.Code != http.StatusOK || w.Header().Get("Content-Language") != info.Code || w.Header().Get("X-Wordlist-Id") != info.ID || w.Header().Get("X-Wordlist-Size") != strconv.Itoa(info.Size) {
				t.Fatalf("wrong HTTP language metadata: %d %v", w.Code, w.Header())
			}

			if !strings.Contains(w.Body.String(), html.EscapeString(want)) || !strings.Contains(w.Body.String(), hex.EncodeToString(digest[:])) || source.reads.Load() == 0 {
				t.Fatal("HTTP words or hash differ from selected dictionary")
			}
		})
	}
}

func TestWordLanguagesRejectBeforeAdmissionAndReads(t *testing.T) {
	for _, query := range []string{"lang=", "lang=unknown", "lang=en-US", "lang=it&lang=it", "lang=it&lang=de", "lang=%zz", "lang=../en", "lang=it;words=2"} {
		source := new(wordTestSource)
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/words?"+query, nil)
		randomWordsHandler(source, 4, 500, 64, true, newHTTPRequestBudget(0, 1))(w, r)

		if w.Code != http.StatusBadRequest || source.reads.Load() != 0 || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("invalid HTTP lang reached admission/entropy: %s %d", query, w.Code)
		}

		budget := newWebSocketBudget(0, 1)
		w = httptest.NewRecorder()
		wsWordsHandler(wordTestDRBG(t), 4, 1, 1024, false, budget)(w, r)

		if w.Code != http.StatusBadRequest || budget.usage().RejectedSize != 0 {
			t.Fatalf("invalid WS lang reached admission: %s %d", query, w.Code)
		}
	}

	for _, query := range []string{"", "lang=EN", "lang=pt", "lang=PT-br", "lang=ko", "lang=zh", "lang=ZH-CN", "lang=zh-TW", "lang=ta", "lang=ar"} {
		if _, err := requestedWordList(query); err != nil {
			t.Fatal(query, err)
		}
	}
}

func TestWordLanguagesLiveWebSocketsAreIndependent(t *testing.T) {
	budget := newWebSocketBudget(16, 16<<20)

	server := httptest.NewServer(wsWordsHandler(wordTestDRBG(t), 4, 1, 1024, false, budget))
	defer server.Close()
	defer waitBudgetActive(t, budget, 0)

	for _, info := range wordlist.Catalog() {
		t.Run(info.Code, func(t *testing.T) {
			dictionary, _ := wordlist.Lookup(info.Code)

			valid := make(map[string]bool, dictionary.Size())
			for i := range dictionary.Size() {
				valid[dictionary.Word(i)] = true
			}

			conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"?words=3&refresh=1&lang="+info.Code, nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if err != nil {
				t.Fatal(err)
			}

			defer conn.Close()

			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

			var frame EntropyFrame
			if err := conn.ReadJSON(&frame); err != nil {
				t.Fatal(err)
			}

			if frame.Language != info.Code || frame.Wordlist != info.ID {
				t.Fatal("wrong websocket language", frame)
			}

			words := strings.Split(frame.Words, " ")
			if len(words) != 3 {
				t.Fatal("wrong word count")
			}

			for _, word := range words {
				if !valid[word] {
					t.Fatal("word from another dictionary", word)
				}
			}

			digest := sha256.Sum256([]byte(frame.Words))
			if frame.Hash != hex.EncodeToString(digest[:]) {
				t.Fatal("UTF-8 digest mismatch")
			}
		})
	}
}

func TestWordLanguageCatalogAndLegacyRedirect(t *testing.T) {
	mux := http.NewServeMux()
	registerWordUI(mux, false)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/wordlists.json", nil))

	var catalog struct {
		Default   string            `json:"default"`
		Languages []wordlist.Info   `json:"languages"`
		Aliases   map[string]string `json:"aliases"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}

	if w.Code != http.StatusOK || catalog.Default != "en" || len(catalog.Languages) != len(wordlist.Catalog()) || catalog.Aliases["zh-cn"] != "zh-Hans" || catalog.Aliases["zh-tw"] != "zh-Hant" {
		t.Fatal("wrong catalog")
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/wordlists.json", nil)
	request.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, request)

	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatal("catalog cache validator failed")
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/words.html?lang=ja&words=6&refresh=50", nil))

	if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "/api-words.html?lang=ja&refresh=50&words=6" {
		t.Fatal("legacy redirect lost settings")
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/wordlist-notices.txt", nil))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Copyright (c) 2013-2016 Pavol Rusnak") || !strings.Contains(w.Body.String(), "Electronic Frontier Foundation") || !strings.Contains(w.Body.String(), "Madan Puraskar Pustakalaya") || !strings.Contains(w.Body.String(), "MOZILLA PUBLIC LICENSE") {
		t.Fatal("wordlist attribution missing")
	}
}
