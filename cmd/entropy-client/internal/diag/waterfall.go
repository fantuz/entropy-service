package diag

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

type waterfallRow struct {
	frame      uint64
	bytes      []byte
	cells      []byte
	cacheWidth int
	cacheColor terminal.Color
}

const (
	WaterfallByte       = "byte"
	WaterfallRGB        = "rgb"
	WaterfallGray       = "gray"
	MaxWaterfallColumns = 512
	MaxWaterfallRows    = 512
)

// Waterfall retains copied prefixes, never transport buffers.
type Waterfall struct {
	width  int
	height int
	mode   string
	rows   []waterfallRow
	line   []byte
}

func NewWaterfall(width, height int, mode string) *Waterfall {
	if mode == "" {
		mode = WaterfallByte
	}

	width, height = max(1, min(width, MaxWaterfallColumns)), max(1, min(height, MaxWaterfallRows))

	return &Waterfall{width: width, height: height, mode: mode, rows: make([]waterfallRow, 0, height)}
}

func (w *Waterfall) bytesPerCell() int {
	if w.mode == WaterfallRGB {
		return 3
	}

	return 1
}

func (w *Waterfall) Add(data []byte, frame uint64) {
	if len(data) == 0 {
		return
	}

	n := min(len(data), w.width*w.bytesPerCell())
	row := waterfallRow{frame: frame, bytes: append([]byte(nil), data[:n]...)}

	if len(w.rows) == w.height {
		row.cells = w.rows[0].cells[:0]
		row.cacheWidth = -1

		copy(w.rows, w.rows[1:])
		w.rows[len(w.rows)-1] = row
	} else {
		w.rows = append(w.rows, row)
	}
}

func byteHue(value byte) (r, g, b int) {
	h := int(value) * 6
	rising := 36 + (h%256)*206/255
	falling := 278 - rising

	switch h / 256 {
	case 0:
		return 242, rising, 36
	case 1:
		return falling, 242, 36
	case 2:
		return 36, 242, rising
	case 3:
		return 36, falling, 242
	case 4:
		return rising, 36, 242
	default:
		return 242, 36, falling
	}
}

func appendCell(out []byte, color terminal.Color, r, g, b int, previous int) ([]byte, int) {
	key := r<<16 | g<<8 | b

	switch color {
	case terminal.TrueColor:
		if key != previous {
			out = append(out, "\033[48;2;"...)
			out = strconv.AppendInt(out, int64(r), 10)
			out = append(out, ';')
			out = strconv.AppendInt(out, int64(g), 10)
			out = append(out, ';')
			out = strconv.AppendInt(out, int64(b), 10)
			out = append(out, 'm')
		}
	case terminal.ANSI256:
		index := 16 + 36*((r*5+127)/255) + 6*((g*5+127)/255) + (b*5+127)/255
		if r == g && g == b {
			switch {
			case r < 8:
				index = 16
			case r > 248:
				index = 231
			default:
				index = 232 + min(23, (r-8+5)/10)
			}
		}

		key = index
		if key != previous {
			out = append(out, "\033[48;5;"...)
			out = strconv.AppendInt(out, int64(index), 10)
			out = append(out, 'm')
		}
	case terminal.ANSI16:
		index := 0
		if r >= 128 {
			index |= 1
		}

		if g >= 128 {
			index |= 2
		}

		if b >= 128 {
			index |= 4
		}

		key = index
		if key != previous {
			out = append(out, "\033["...)
			out = strconv.AppendInt(out, int64(40+index), 10)
			out = append(out, 'm')
		}
	case terminal.Monochrome:
		const ramp = " .:-=+*#%@"

		value := (2126*r + 7152*g + 722*b) / 10000
		level := ramp[value*(len(ramp)-1)/255]

		return append(out, level, level), key
	}

	return append(out, ' ', ' '), key
}

// Render uses two columns per cell. Profile.Rows is the available screen budget.
func (w *Waterfall) Render(out io.Writer, profile terminal.Profile) {
	w.RenderView(out, profile, w.width, w.height)
}

// RenderView changes only visible dimensions, retaining bounded original rows.
func (w *Waterfall) RenderView(out io.Writer, profile terminal.Profile, columns, rows int) {
	if len(w.rows) == 0 {
		return
	}

	label := max(6, len(strconv.FormatUint(w.rows[len(w.rows)-1].frame, 10)))

	width, height := min(w.width, max(1, columns)), min(len(w.rows), max(1, rows))
	if profile.Columns > 0 {
		width = min(width, (profile.Columns-label-4)/2)
		if width < 1 {
			return
		}
	}

	stride := w.bytesPerCell()

	mode := w.mode
	if profile.Color == terminal.Monochrome && mode == WaterfallByte {
		mode = "gray fallback"
	}

	heading := fmt.Sprintf("\nWaterfall | %s | newest first\nOffsets [0,%d) | %d byte(s)/cell | frame #\n", mode, width*stride, stride)
	if profile.Rows > 0 {
		available := profile.Rows - terminal.TextRows(heading, profile.Columns)
		if available < 1 {
			return
		}

		height = min(height, available)
	}

	fmt.Fprint(out, heading)

	for i := len(w.rows) - 1; i >= len(w.rows)-height; i-- {
		var digits [20]byte

		number := strconv.AppendUint(digits[:0], w.rows[i].frame, 10)
		w.line = append(w.line[:0], '#')
		w.line = appendSpaces(w.line, label-len(number))
		w.line = append(w.line, number...)
		w.line = append(w.line, " | "...)
		w.line = append(w.line, w.rowCells(i, width, profile.Color)...)
		w.line = append(w.line, '\n')
		_, _ = out.Write(w.line)
	}
}

// RenderCanvas fills the available rows with newest-first cells, without labels.
// Missing samples/bytes remain blank; no samples are invented to fill space.
func (w *Waterfall) RenderCanvas(out *strings.Builder, profile terminal.Profile, rows int) {
	_, _ = out.Write(w.AppendCanvas(nil, profile, rows))
}

// AppendCanvas reuses the caller's bounded frame buffer and immutable row caches.
// Only a newly sampled row, changed visible width or changed palette is encoded.
func (w *Waterfall) AppendCanvas(dst []byte, profile terminal.Profile, rows int) []byte {
	columns, height := min(max(0, profile.Columns), MaxWaterfallColumns*2), min(max(0, profile.Rows), MaxWaterfallRows)

	width := min(w.width, columns/2)
	if columns == 0 || height == 0 {
		return dst
	}

	// Reserve the worst encoded size once. The buffer stays bounded on resize.
	need := len(dst) + height*(width*cellSize(profile.Color)+columns-width*2+9)
	if cap(dst) < need {
		buffer := make([]byte, len(dst), need)
		copy(buffer, dst)
		dst = buffer
	}

	for y := range height {
		if y < min(len(w.rows), rows) {
			dst = append(dst, w.rowCells(len(w.rows)-1-y, width, profile.Color)...)
			dst = appendSpaces(dst, columns-width*2)
		} else {
			dst = appendSpaces(dst, columns)
		}

		dst = append(dst, '\n')
	}

	return dst
}

func cellSize(color terminal.Color) int {
	switch color {
	case terminal.TrueColor:
		return 24
	case terminal.ANSI256:
		return 16
	case terminal.ANSI16:
		return 8
	case terminal.Monochrome:
		return 2
	default:
		return 2
	}
}

func appendSpaces(dst []byte, n int) []byte {
	const spaces = "                                                                "
	for n > 0 {
		count := min(n, len(spaces))
		dst = append(dst, spaces[:count]...)
		n -= count
	}

	return dst
}

func (w *Waterfall) rowCells(index, width int, color terminal.Color) []byte {
	row := &w.rows[index]
	if row.cells != nil && row.cacheWidth == width && row.cacheColor == color {
		return row.cells
	}

	need := width*cellSize(color) + 8
	if cap(row.cells) < need {
		row.cells = make([]byte, 0, need)
	}

	row.cells = w.appendCells(row.cells[:0], row.bytes, width, color)
	row.cacheWidth, row.cacheColor = width, color

	return row.cells
}

func (w *Waterfall) appendCells(line, data []byte, width int, color terminal.Color) []byte {
	stride := w.bytesPerCell()
	previous := -1

	for x := range width {
		offset := x * stride
		if offset+stride > len(data) {
			if color != terminal.Monochrome {
				line = append(line, "\033[0m"...)
			}

			line = appendSpaces(line, (width-x)*2)

			break
		}

		r, g, b := int(data[offset]), int(data[offset]), int(data[offset])
		if w.mode == WaterfallRGB {
			g, b = int(data[offset+1]), int(data[offset+2])
		} else if w.mode == WaterfallByte && color != terminal.Monochrome {
			r, g, b = byteHue(data[offset])
		}

		line, previous = appendCell(line, color, r, g, b, previous)
	}

	if color != terminal.Monochrome {
		line = append(line, "\033[0m"...)
	}

	return line
}
