// Package wordlist contains pinned, attributed dictionaries for random-word sampling.
package wordlist

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed manifest.json data/*.txt NOTICE.txt
var files embed.FS

type Info struct {
	Code                 string  `json:"code"`
	Name                 string  `json:"name"`
	NativeName           string  `json:"native_name"`
	ID                   string  `json:"id"`
	Kind                 string  `json:"kind"`
	Title                string  `json:"title"`
	Size                 int     `json:"size"`
	SHA256               string  `json:"sha256"`
	SourceURL            string  `json:"source_url"`
	SourceSHA256         string  `json:"source_sha256"`
	SourceFormat         string  `json:"source_format"`
	Author               string  `json:"author"`
	License              string  `json:"license"`
	LicenseURL           string  `json:"license_url"`
	SelectionBitsPerWord float64 `json:"selection_bits_per_word"`
}

type Dictionary struct {
	info     Info
	words    []string
	maxBytes int
}

var dictionaries = load()

func load() []*Dictionary {
	data, err := files.ReadFile("manifest.json")
	if err != nil {
		panic(err)
	}

	var metadata []Info
	if err := json.Unmarshal(data, &metadata); err != nil {
		panic(err)
	}

	result := make([]*Dictionary, 0, len(metadata))
	for _, info := range metadata {
		data, err := files.ReadFile("data/" + info.Code + ".txt")
		if err != nil {
			panic(err)
		}

		dictionary, err := parse(info, data)
		if err != nil {
			panic(err)
		}

		result = append(result, dictionary)
	}

	return result
}

func parse(info Info, data []byte) (*Dictionary, error) {
	digest := sha256.Sum256(data)
	if !utf8.Valid(data) || hex.EncodeToString(digest[:]) != info.SHA256 || !strings.HasSuffix(string(data), "\n") {
		return nil, fmt.Errorf("wordlist %s integrity or UTF-8 check failed", info.Code)
	}

	words := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(words) != info.Size || len(words) < 2 {
		return nil, fmt.Errorf("wordlist %s size mismatch", info.Code)
	}

	seen := make(map[string]bool, len(words))
	longest := 0

	for _, word := range words {
		if word == "" || seen[word] || len(word) > 128 {
			return nil, fmt.Errorf("wordlist %s has an empty, duplicate or overlong entry", info.Code)
		}

		for _, r := range word {
			if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return nil, fmt.Errorf("wordlist %s contains unsafe separators/controls", info.Code)
			}
		}

		seen[word] = true
		longest = max(longest, len(word))
	}

	info.SelectionBitsPerWord = math.Log2(float64(len(words)))

	return &Dictionary{info: info, words: words, maxBytes: longest}, nil
}

func Default() *Dictionary { return dictionaries[0] }

func Lookup(code string) (*Dictionary, bool) {
	if strings.EqualFold(code, "pt") {
		code = "pt-BR"
	}

	for _, dictionary := range dictionaries {
		if strings.EqualFold(code, dictionary.info.Code) {
			return dictionary, true
		}
	}

	return nil, false
}

func Catalog() []Info {
	result := make([]Info, 0, len(dictionaries))
	for _, dictionary := range dictionaries {
		result = append(result, dictionary.info)
	}

	return result
}

func MaxWordBytes() int {
	longest := 0
	for _, dictionary := range dictionaries {
		longest = max(longest, dictionary.maxBytes)
	}

	return longest
}

func (d *Dictionary) Size() int             { return len(d.words) }
func (d *Dictionary) Word(index int) string { return d.words[index] }
func (d *Dictionary) Info() Info            { return d.info }
func Notices() []byte {
	data, err := files.ReadFile("NOTICE.txt")
	if err != nil {
		panic(err)
	}

	return data
}
