package diag

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func renderWaterfall(w *Waterfall, profile terminal.Profile) string {
	var out strings.Builder
	w.Render(&out, profile)

	return out.String()
}

func TestWaterfallBoundsAndCopiesHistory(t *testing.T) {
	w := NewWaterfall(1000, 1000, WaterfallRGB)
	data := bytes.Repeat([]byte{42}, 1<<20)

	for frame := uint64(1); frame <= MaxWaterfallRows+36; frame++ {
		data[0] = byte(frame)
		w.Add(data, frame)
	}

	clear(data)
	w.Add(nil, MaxWaterfallRows+37)

	if w.width != MaxWaterfallColumns || w.height != MaxWaterfallRows || len(w.rows) != MaxWaterfallRows || cap(w.rows) > MaxWaterfallRows {
		t.Fatalf("history is not bounded: width=%d height=%d len=%d cap=%d", w.width, w.height, len(w.rows), cap(w.rows))
	}

	for i, row := range w.rows {
		if row.frame != uint64(37+i) || len(row.bytes) != MaxWaterfallColumns*3 || cap(row.bytes) > MaxWaterfallColumns*3 || row.bytes[0] != byte(row.frame) {
			t.Fatalf("history retained excess bytes or aliased a transport buffer: frame=%d bytes=%d cap=%d", row.frame, len(row.bytes), cap(row.bytes))
		}
	}
}

func TestWaterfallMappings(t *testing.T) {
	for _, tc := range []struct {
		mode string
		data []byte
		want string
	}{
		{WaterfallRGB, []byte{255, 0, 0, 0, 255, 0, 0, 0, 255}, "\x1b[48;2;255;0;0m  \x1b[48;2;0;255;0m  \x1b[48;2;0;0;255m  "},
		{WaterfallGray, []byte{0, 128, 255}, "\x1b[48;2;0;0;0m  \x1b[48;2;128;128;128m  \x1b[48;2;255;255;255m  "},
		{WaterfallByte, []byte{0, 128, 255}, "\x1b[48;2;242;36;36m  \x1b[48;2;36;242;242m  \x1b[48;2;242;36;41m  "},
	} {
		w := NewWaterfall(3, 1, tc.mode)
		w.Add(tc.data, 1)

		out := renderWaterfall(w, terminal.Profile{Color: terminal.TrueColor})
		if !strings.Contains(out, tc.want) || !strings.HasSuffix(out, "\x1b[0m\n") {
			t.Errorf("%s mapping/reset incorrect: %q", tc.mode, out)
		}
	}
}

func TestWaterfallIncompleteRGBTripletsAreBlank(t *testing.T) {
	for _, n := range []int{1, 2, 4, 5, 7, 8} {
		w := NewWaterfall(3, 1, WaterfallRGB)
		w.Add(bytes.Repeat([]byte{255}, n), 1)

		out := renderWaterfall(w, terminal.Profile{Color: terminal.TrueColor})
		if strings.Count(out, "\x1b[48;2;") != n/3 {
			t.Errorf("partial triplet was painted: bytes=%d output=%q", n, out)
		}
	}
}

func TestWaterfallFallbackPalettes(t *testing.T) {
	for _, tc := range []struct {
		color terminal.Color
		want  string
	}{
		{terminal.TrueColor, "\x1b[48;2;255;0;0m  "},
		{terminal.ANSI256, "\x1b[48;5;196m  "},
		{terminal.ANSI16, "\x1b[41m  "},
		{terminal.Monochrome, ".."},
	} {
		w := NewWaterfall(1, 1, WaterfallRGB)
		w.Add([]byte{255, 0, 0}, 1)

		out := renderWaterfall(w, terminal.Profile{Color: tc.color})
		if !strings.Contains(out, tc.want) {
			t.Errorf("palette %d: %q", tc.color, out)
		}
	}

	w := NewWaterfall(3, 1, WaterfallByte)
	w.Add([]byte{0, 128, 255}, 1)

	out := renderWaterfall(w, terminal.Profile{})
	if strings.ContainsRune(out, '\x1b') || !strings.Contains(out, "gray fallback") || !strings.HasSuffix(out, "  ==@@\n") {
		t.Fatalf("monochrome fallback = %q", out)
	}
}

func TestWaterfallGrayscalePaletteEndpoints(t *testing.T) {
	w := NewWaterfall(2, 1, WaterfallGray)
	w.Add([]byte{0, 255}, 1)

	out := renderWaterfall(w, terminal.Profile{Color: terminal.ANSI256})
	if !strings.Contains(out, "\x1b[48;5;16m  \x1b[48;5;231m  ") {
		t.Fatalf("256-color grayscale lost black/white endpoints: %q", out)
	}
}

func TestWaterfallResizeKeepsNewestHistory(t *testing.T) {
	w := NewWaterfall(128, 32, WaterfallByte)
	for i := uint64(1); i <= 32; i++ {
		w.Add(bytes.Repeat([]byte{byte(i)}, 128), i)
	}

	for _, columns := range []int{80, 60, 18, 80} {
		t.Run(strconv.Itoa(columns), func(t *testing.T) {
			out := renderWaterfall(w, terminal.Profile{Columns: columns, Rows: 8})
			if terminal.TextRows(out, columns) > 8 {
				t.Fatalf("waterfall exceeded height: %q", out)
			}

			previous := 0

			for line := range strings.SplitSeq(out, "\n") {
				if !strings.HasPrefix(line, "#") {
					continue
				}

				var frame int
				if _, err := fmt.Sscanf(line, "#%d", &frame); err != nil {
					t.Fatal(err)
				}

				if len(line) > columns || (previous == 0 && frame != 32) || (previous != 0 && frame != previous-1) {
					t.Fatalf("row width/order incorrect: %q", line)
				}

				previous = frame
			}

			if previous == 0 || len(w.rows) != 32 || len(w.rows[0].bytes) != 128 {
				t.Fatal("resizing destroyed retained history or rendered no rows")
			}
		})
	}
}

func TestWaterfallTinyTerminal(t *testing.T) {
	w := NewWaterfall(8, 1, WaterfallGray)
	w.Add([]byte{128}, 1)

	for _, profile := range []terminal.Profile{{Columns: 9, Rows: 24}, {Columns: 80, Rows: 1}} {
		if out := renderWaterfall(w, profile); out != "" {
			t.Errorf("no space for a heading and cell, should omit waterfall: %q", out)
		}
	}
}
