package main

import (
	"errors"
	"io"
	"net/http"
	"strconv"
)

// requestEntropyReader checks cancellation between the bounded io.CopyN reads.
// It cannot interrupt an individual blocked Read or a blocked socket Write;
// normal HTTP write deadlines and transport cancellation are still required.
type requestEntropyReader struct {
	check  func() error
	source io.Reader
}

func (r requestEntropyReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, err
	}

	return r.source.Read(p)
}

// streamEntropyResponse generates exactly count bytes on success, without
// allocating a response-sized buffer. A failed stream may already have sent
// headers/body; callers must log errors rather than invoke http.Error.
func streamEntropyResponse(w http.ResponseWriter, r *http.Request, source io.Reader, count int) error {
	if count <= 0 {
		return errors.New("entropy response size must be positive")
	}

	if err := r.Context().Err(); err != nil {
		return err
	}

	w.Header().Set("Content-Length", strconv.Itoa(count))
	_, err := io.CopyN(w, requestEntropyReader{check: r.Context().Err, source: source}, int64(count))

	return err
}
