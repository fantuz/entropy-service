package diag

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func TestWaterfallCacheMatchesFreshRowsAfterResizePaletteAndEviction(t *testing.T) {
	for _, mode := range []string{WaterfallByte, WaterfallRGB, WaterfallGray} {
		w := NewWaterfall(32, 3, mode)

		for frame := uint64(1); frame <= 12; frame++ {
			data := []byte{byte(frame), 0, 255, 128, 31, byte(frame + 10), 1}
			w.Add(data, frame)

			for _, color := range []terminal.Color{terminal.TrueColor, terminal.ANSI256, terminal.ANSI16, terminal.Monochrome, terminal.TrueColor} {
				for _, columns := range []int{64, 13, 32, 64} {
					profile := terminal.Profile{Columns: columns, Rows: 6, Color: color}

					fresh := NewWaterfall(32, 3, mode)
					for _, row := range w.rows {
						fresh.Add(row.bytes, row.frame)
					}

					got := w.AppendCanvas(nil, profile, 3)

					want := fresh.AppendCanvas(nil, profile, 3)
					if !bytes.Equal(got, want) {
						t.Fatalf("stale cache: mode=%s frame=%d color=%d columns=%d", mode, frame, color, columns)
					}

					if !bytes.Equal(w.AppendCanvas(nil, profile, 3), want) {
						t.Fatal("cache reuse changed pixels")
					}
				}
			}

			clear(data)
		}
	}
}

func TestWaterfallCacheAndCanvasRemainBounded(t *testing.T) {
	w := NewWaterfall(MaxWaterfallColumns, MaxWaterfallRows, WaterfallRGB)

	data := bytes.Repeat([]byte{1, 123, 255}, MaxWaterfallColumns)
	for frame := range uint64(MaxWaterfallRows + 4) {
		w.Add(data, frame)
	}

	profile := terminal.Profile{Columns: 1000000, Rows: 1000000, Color: terminal.TrueColor}

	frame := w.AppendCanvas(nil, profile, MaxWaterfallRows)
	if strings.Count(string(frame), "\n") != MaxWaterfallRows || cap(frame) > MaxWaterfallRows*(MaxWaterfallColumns*24+9) {
		t.Fatalf("unbounded frame: len=%d cap=%d", len(frame), cap(frame))
	}

	for _, row := range w.rows {
		if cap(row.cells) > MaxWaterfallColumns*24+8 {
			t.Fatal("unbounded retained row encoding")
		}
	}

	// AppendCanvas may reuse a caller's buffer but must not retain that buffer
	// in its row caches. A subsequent write cannot corrupt cached history.
	want := bytes.Clone(frame)
	clear(frame)

	frame = w.AppendCanvas(frame[:0], profile, MaxWaterfallRows)
	if !bytes.Equal(frame, want) {
		t.Fatal("caller frame storage was retained as row-cache data")
	}

	next := w.AppendCanvas(frame[:0], terminal.Profile{Columns: 17, Rows: 3}, 1)
	if bytes.ContainsRune(next, '\x1b') || bytes.Count(next, []byte("\n")) != 3 {
		t.Fatal("palette/size change retained old frame or styling")
	}
}
