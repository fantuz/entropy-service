package wordlist

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

func TestPinnedLanguageCatalogAndUTF8Bounds(t *testing.T) {
	want := map[string]int{"en": 7776, "it": 8192, "fr": 7776, "de": 7776, "es": 8192, "pt-BR": 7776, "ca": 8192, "cs": 2048, "ja": 2048, "ko": 2048}

	if Default().Info().SHA256 != "6d557f0693958fb5e650b68b5bee585eb82cf4da32965505c789e924743bc522" {
		t.Fatal("English list differs from the pre-migration EFF dictionary")
	}

	catalog := Catalog()
	if len(catalog) != len(want) {
		t.Fatal("expected ten languages")
	}

	for _, info := range catalog {
		if info.Size != want[info.Code] || math.Abs(info.SelectionBitsPerWord-math.Log2(float64(info.Size))) > 1e-12 {
			t.Fatalf("wrong list size/selection space: %+v", info)
		}

		if info.Author == "" || info.License == "" || !strings.HasPrefix(info.SourceURL, "https://raw.githubusercontent.com/") || len(info.SourceSHA256) != 64 || len(info.SHA256) != 64 {
			t.Fatal("missing provenance", info.Code)
		}

		if strings.Contains(info.SourceURL, "/master/") || strings.Contains(info.SourceURL, "/main/") {
			t.Fatal("wordlist source must be pinned")
		}

		dictionary, ok := Lookup(info.Code)
		if !ok || dictionary.Size() != info.Size {
			t.Fatal(info.Code)
		}

		for i := range dictionary.Size() {
			if len(dictionary.Word(i)) > MaxWordBytes() {
				t.Fatal("admission bound ignores UTF-8 bytes")
			}
		}
	}

	if Default().Info().Code != "en" || Default().Word(0) != "abacus" || Default().Word(1) != "abdomen" || Default().Word(7775) != "zoom" {
		t.Fatal("English compatibility changed")
	}

	if MaxWordBytes() <= 9 {
		t.Fatal("multibyte dictionaries must influence memory reservations")
	}

	for _, code := range []string{"EN", "PT", "pt-br", "JA"} {
		if _, ok := Lookup(code); !ok {
			t.Fatal(code)
		}
	}

	for _, code := range []string{"", "unknown", "en-US", "../en"} {
		if _, ok := Lookup(code); ok {
			t.Fatal("accepted unsupported code", code)
		}
	}

	catalog[0].Name = "modified"
	if Default().Info().Name != "English" {
		t.Fatal("catalog mutation changed shared dictionary")
	}
}

func TestDictionaryIntegrityRefusesCorruption(t *testing.T) {
	for _, data := range [][]byte{[]byte("same\nsame\n"), []byte("one\nbad word\n"), []byte("one\nbad\x1b\n"), []byte("one\nbad\u202e\n"), {0xff, '\n', 'a', '\n'}, []byte("one\ntwo"), []byte("one\n" + strings.Repeat("x", 129) + "\n")} {
		digest := sha256.Sum256(data)

		info := Info{Code: "fixture", Size: 2, SHA256: hex.EncodeToString(digest[:])}
		if _, err := parse(info, data); err == nil {
			t.Fatalf("accepted invalid dictionary %q", data)
		}
	}

	if _, err := parse(Info{Code: "fixture", Size: 2, SHA256: strings.Repeat("0", 64)}, []byte("one\ntwo\n")); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}
