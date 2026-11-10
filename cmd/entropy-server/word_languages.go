package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/wordlist"
)

const wordPagePath = "/api-words.html"

func requestedWordList(rawQuery string) (*wordlist.Dictionary, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, errors.New("invalid word query encoding")
	}

	values, present := query["lang"]
	if !present {
		return wordlist.Default(), nil
	}

	if len(values) != 1 || values[0] == "" {
		return nil, errors.New("lang must be supplied once with a supported language code")
	}

	dictionary, ok := wordlist.Lookup(values[0])
	if !ok {
		return nil, errors.New("unsupported lang; see /wordlists.json for supported codes")
	}

	return dictionary, nil
}

func registerWordUI(mux *http.ServeMux) {
	catalog, err := json.Marshal(struct {
		Default   string          `json:"default"`
		Languages []wordlist.Info `json:"languages"`
	}{Default: wordlist.Default().Info().Code, Languages: wordlist.Catalog()})
	if err != nil {
		panic(err)
	}

	mux.Handle("GET /wordlists.json", wordResource("wordlists.json", "application/json; charset=utf-8", catalog))
	mux.Handle("GET /wordlist-notices.txt", wordResource("wordlist-notices.txt", "text/plain; charset=utf-8", wordlist.Notices()))
	mux.HandleFunc("GET /words.html", func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, "invalid word query encoding", http.StatusBadRequest)
			return
		}

		target := url.URL{Path: wordPagePath, RawQuery: query.Encode()}
		http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
	})
}

func wordResource(name, contentType string, data []byte) http.Handler {
	digest := sha256.Sum256(data)
	etag := "\"" + hex.EncodeToString(digest[:]) + "\""

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
