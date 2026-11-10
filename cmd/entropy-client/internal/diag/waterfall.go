package diag

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

type waterfallRow struct {
	frame uint64
	bytes []byte
}

const (
	WaterfallByte = "byte"
	WaterfallRGB  = "rgb"
	WaterfallGray = "gray"
)

// Waterfall retains copied prefixes, never transport buffers.
type Waterfall struct {
	width  int
	height int
	mode   string
	rows   []waterfallRow
}

func NewWaterfall(width, height int, mode string) *Waterfall {
	if mode == "" {
		mode = WaterfallByte
	}

	width, height = max(1, min(width, 128)), max(1, min(height, 32))

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

func cell(out *strings.Builder, color terminal.Color, r, g, b int) {
	switch color {
	case terminal.TrueColor:
		fmt.Fprintf(out, "\033[48;2;%d;%d;%dm  ", r, g, b)
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

		fmt.Fprintf(out, "\033[48;5;%dm  ", index)
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

		fmt.Fprintf(out, "\033[%dm  ", 40+index)
	case terminal.Monochrome:
		const ramp = " .:-=+*#%@"

		value := (2126*r + 7152*g + 722*b) / 10000
		level := ramp[value*(len(ramp)-1)/255]
		out.WriteByte(level)
		out.WriteByte(level)
	}
}

// Render uses two columns per cell. Profile.Rows is the available screen budget.
func (w *Waterfall) Render(out io.Writer, profile terminal.Profile) {
	if len(w.rows) == 0 {
		return
	}

	label := max(6, len(strconv.FormatUint(w.rows[len(w.rows)-1].frame, 10)))

	width, height := w.width, len(w.rows)
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
		row := w.rows[i]

		var line strings.Builder

		fmt.Fprintf(&line, "#%*d | ", label, row.frame)

		for x := range width {
			offset := x * stride
			if offset+stride > len(row.bytes) {
				if profile.Color != terminal.Monochrome {
					line.WriteString("\033[0m")
				}

				line.WriteString("  ")

				continue
			}

			r, g, b := int(row.bytes[offset]), int(row.bytes[offset]), int(row.bytes[offset])
			if w.mode == WaterfallRGB {
				g, b = int(row.bytes[offset+1]), int(row.bytes[offset+2])
			} else if w.mode == WaterfallByte && profile.Color != terminal.Monochrome {
				r, g, b = byteHue(row.bytes[offset])
			}

			cell(&line, profile.Color, r, g, b)
		}

		if profile.Color != terminal.Monochrome {
			line.WriteString("\033[0m")
		}

		fmt.Fprintln(out, line.String())
	}
}
