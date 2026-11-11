package wordlist

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestPinnedLanguageCatalogAndUTF8Bounds(t *testing.T) {
	want := map[string]int{
		"en": 7776, "it": 8192, "fr": 7776, "de": 7776, "es": 8192, "pt-BR": 7776, "ca": 8192,
		"cs": 2048, "ja": 2048, "ko": 2048, "ru": 2048, "af": 8192, "tr": 2048,
		"zh-Hans": 2048, "zh-Hant": 2048, "ta": 8192, "ne": 8192, "ar": 8192,
	}

	if Default().Info().SHA256 != "6d557f0693958fb5e650b68b5bee585eb82cf4da32965505c789e924743bc522" {
		t.Fatal("English list differs from the pre-migration EFF dictionary")
	}

	catalog := Catalog()
	if len(catalog) != len(want) {
		t.Fatalf("expected %d dictionary choices, got %d", len(want), len(catalog))
	}

	for _, info := range catalog {
		if info.Size != want[info.Code] || math.Abs(info.SelectionBitsPerWord-math.Log2(float64(info.Size))) > 1e-12 {
			t.Fatalf("wrong list size/selection space: %+v", info)
		}

		delete(want, info.Code)

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

	if len(want) != 0 {
		t.Fatal("catalog omits dictionaries", want)
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

func TestDictionaryAliasesAreCanonicalAndImmutable(t *testing.T) {
	for alias, code := range map[string]string{
		"PT": "pt-BR", "pt-br": "pt-BR", "RU": "ru", "AF": "af", "TR": "tr",
		"zh": "zh-Hans", "ZH-CN": "zh-Hans", "zh-SG": "zh-Hans", "zh-hans": "zh-Hans",
		"ZH-TW": "zh-Hant", "zh-HK": "zh-Hant", "zh-mo": "zh-Hant", "zh-hant": "zh-Hant",
	} {
		dictionary, ok := Lookup(alias)
		if !ok || dictionary.Info().Code != code {
			t.Fatalf("alias %q must resolve to %q", alias, code)
		}
	}

	aliases := Aliases()
	aliases["zh"] = "en"
	delete(aliases, "pt")

	dictionary, _ := Lookup("zh")
	if dictionary.Info().Code != "zh-Hans" || Aliases()["pt"] != "pt-BR" {
		t.Fatal("caller changed global aliases")
	}

	simplified, _ := Lookup("zh-Hans")

	traditional, _ := Lookup("zh-Hant")
	if simplified.Info().ID == traditional.Info().ID || simplified.Info().SHA256 == traditional.Info().SHA256 {
		t.Fatal("Chinese writing variants must have distinct identities")
	}
}

func TestDictionarySpellingSubsetWordContracts(t *testing.T) {
	for _, code := range []string{"af", "ta", "ne", "ar"} {
		dictionary, _ := Lookup(code)
		if dictionary.Info().Kind != "spelling-word-data" || dictionary.Size() != 8192 {
			t.Fatal("spelling subset metadata", code)
		}

		previous := ""

		for i := range dictionary.Size() {
			word := dictionary.Word(i)

			size := utf8.RuneCountInString(word)
			if size < 3 || size > 12 || code == "af" && size < 4 || word <= previous {
				t.Fatalf("subset %s length/order contract: %q", code, word)
			}

			for _, r := range word {
				if !unicode.IsLetter(r) && !unicode.IsMark(r) || code == "af" && !unicode.IsLower(r) {
					t.Fatalf("subset %s has unexpected character %q", code, r)
				}
			}

			previous = word
		}
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
