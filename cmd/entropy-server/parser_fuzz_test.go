package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"net/url"
	"testing"
)

func FuzzGenerationQuery(f *testing.F) {
	for _, seed := range []string{"", "bytes=1", "bytes=33554433", "bytes=1&bytes=2", "bytes=%zz", "bytes=-1&format=compact", "format=json&format=hash"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}

		request := &http.Request{URL: &url.URL{RawQuery: raw}}

		n, err := extractByteCount(request, 32, 64)
		if err == nil && (n < 1 || n > 64) {
			t.Fatalf("accepted out-of-bounds quantity %d", n)
		}

		format, err := parseByteFrameFormat(request.URL.Query())
		if err == nil && format != byteFrameJSON && format != byteFrameCompact && format != byteFrameHash {
			t.Fatal("unknown format accepted")
		}
	})
}

func FuzzUploadAnalysis(f *testing.F) {
	f.Add([]byte{}, uint16(0))
	f.Add([]byte{0, 1, 0xff}, uint16(1))
	f.Add(bytes.Repeat([]byte{3}, 4096), uint16(31))
	f.Fuzz(func(t *testing.T, data []byte, requested uint16) {
		if len(data) > 64<<10 {
			t.Skip()
		}

		limit := int(requested) + 1

		result, err := analyzeUpload(t.Context(), bytes.NewReader(data), limit)
		if len(data) == 0 {
			if err == nil {
				t.Fatal("accepted empty upload")
			}

			return
		}

		if err != nil {
			t.Fatal(err)
		}

		digest := sha256.Sum256(data)
		if result.TotalBytes != int64(len(data)) || result.AnalyzedBytes != int64(min(len(data), limit)) || result.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal("upload prefix accounting or full-body hash changed")
		}

		var histogram [256]uint64
		for _, b := range data[:min(len(data), limit)] {
			histogram[b]++
		}

		if result.Histogram != histogram || result.Ones+result.Zeros != uint64(result.AnalyzedBytes)*8 || math.IsNaN(result.Entropy) || result.Entropy < 0 || result.Entropy > 8 {
			t.Fatal("invalid upload analysis")
		}

		if !bytes.Equal(result.Preview, data[:min(len(data), limit, uploadPreviewBytes)]) {
			t.Fatal("preview exceeds or differs from analyzed prefix")
		}
	})
}
