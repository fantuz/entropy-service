package metrics

import (
	"io"
)

func CountWrite(w io.Writer, data []byte) (int, error) {
	return w.Write(data)
}

// metrics.CountWrite(w, payload)
