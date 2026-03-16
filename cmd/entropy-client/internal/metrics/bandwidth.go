package metrics

import (
	"io"
	//"github.com/fantuz/entropy-service/entropy-client/internal/diag"
	//"github.com/fantuz/entropy-service/entropy-client/internal/stats"
)

func CountWrite(w io.Writer, data []byte) (int, error) {
    n, err := w.Write(data)
    if err == nil {
        //AddGeneratedBytes(uint64(n))
    }
    return n, err
}

//metrics.CountWrite(w, payload)
