package rdrand

import "encoding/binary"

// Reader obtains bulk output directly from RDRAND, without seed mixing,
// conditioning or fallback. Concurrent reads are supported.
type Reader struct {
	next func() (uint64, error)
}

func NewReader() (*Reader, error) {
	if !Available() {
		return nil, ErrUnsupported
	}

	return &Reader{next: Uint64}, nil
}

// Read fills p in little-endian word order. Each word has the instruction's
// existing bounded retry budget. Failure clears the entire destination and
// returns zero bytes, including when earlier words succeeded. Unused bytes of
// the last word are discarded; splitting reads need not preserve a sequence.
func (r *Reader) Read(p []byte) (int, error) {
	for offset := 0; offset < len(p); offset += 8 {
		value, err := r.next()
		if err != nil {
			clear(p)

			return 0, err
		}

		if len(p)-offset >= 8 {
			binary.LittleEndian.PutUint64(p[offset:], value)
		} else {
			for i := offset; i < len(p); i++ {
				p[i] = byte(value)
				value >>= 8
			}
		}
	}

	return len(p), nil
}
