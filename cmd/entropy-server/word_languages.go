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

// A query always wins, including an invalid query. Only a missing lang may
// fall back to a validated, opt-in cookie; old or unknown values mean English.
func preferredWordList(r *http.Request, hasCookie bool) (*wordlist.Dictionary, error) {
	dictionary, err := requestedWordList(r.URL.RawQuery)
	if err != nil || !hasCookie || r.URL.Query().Has("lang") {
		return dictionary, err
	}

	if prefs, err := getPrefsCookie(r); err == nil {
		if selected, ok := wordlist.Lookup(prefs.Lang); ok {
			return selected, nil
		}
	}

	return dictionary, nil
}

func registerWordUI(mux *http.ServeMux, hasCookie bool) {
	metadata := struct {
		Default   string            `json:"default"`
		Languages []wordlist.Info   `json:"languages"`
		Aliases   map[string]string `json:"aliases"`
		Selected  string            `json:"selected,omitempty"`
	}{Default: wordlist.Default().Info().Code, Languages: wordlist.Catalog(), Aliases: wordlist.Aliases()}

	catalog, err := json.Marshal(metadata)
	if err != nil {
		panic(err)
	}

	if !hasCookie {
		mux.Handle("GET /wordlists.json", wordResource("wordlists.json", "application/json; charset=utf-8", catalog))
	} else {
		mux.HandleFunc("GET /wordlists.json", func(w http.ResponseWriter, r *http.Request) {
			// Never share a remembered language through an intermediary cache.
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("Vary", "Cookie")

			dictionary, err := preferredWordList(r, true)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			current := metadata
			current.Selected = dictionary.Info().Code

			w.Header().Set("Content-Type", "application/json; charset=utf-8")

			if r.Method != http.MethodHead {
				_ = json.NewEncoder(w).Encode(current)
			}
		})
	}

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
