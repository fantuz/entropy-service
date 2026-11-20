package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"

	"github.com/gorilla/websocket"
)

type byteFrameFormat string

const (
	byteFrameJSON    byteFrameFormat = "json"
	byteFrameCompact byteFrameFormat = "compact"
	byteFramePreview byteFrameFormat = "preview"
	byteFrameHash    byteFrameFormat = "hash"

	previewPrefixBytes = 256
	previewHeaderBytes = 4 + sha256.Size
)

func parseByteFrameFormat(query url.Values) (byteFrameFormat, error) {
	values, present := query["format"]
	if !present {
		return byteFrameJSON, nil
	}

	if len(values) == 1 {
		switch format := byteFrameFormat(values[0]); format {
		case byteFrameJSON, byteFrameCompact, byteFramePreview, byteFrameHash:
			return format, nil
		}
	}

	return "", errors.New("format must be supplied once as json, compact, preview or hash")
}

func writeByteFrame(conn *websocket.Conn, data []byte, format byteFrameFormat) error {
	kind := websocket.TextMessage
	if format == byteFrameCompact || format == byteFramePreview {
		kind = websocket.BinaryMessage
	}

	return writeObservedPayload(conn, kind, func(w io.Writer) error {
		return encodeByteFrame(w, data, format)
	})
}

func encodeByteFrame(w io.Writer, data []byte, format byteFrameFormat) error {
	digest := sha256.Sum256(data)

	switch format {
	case byteFramePreview:
		// Preview v1: full sample length (big endian), its digest, then its prefix.
		var frame [previewHeaderBytes + previewPrefixBytes]byte
		binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
		copy(frame[4:previewHeaderBytes], digest[:])
		n := copy(frame[previewHeaderBytes:], data)
		_, err := w.Write(frame[:previewHeaderBytes+n])

		return err
	case byteFrameCompact:
		// Compact v1: one binary message containing the 32-byte SHA-256 digest
		// followed by the requested raw bytes. Separate writes avoid a large copy.
		if _, err := w.Write(digest[:]); err != nil {
			return err
		}

		_, err := w.Write(data)

		return err
	case byteFrameHash:
		// A colour display needs the digest, not two encodings of every byte.
		return json.NewEncoder(w).Encode(struct {
			Hash  string `json:"hash"`
			Bytes int    `json:"bytes"`
		}{Hash: hex.EncodeToString(digest[:]), Bytes: len(data)})
	case byteFrameJSON:
		return writeByteFrameJSON(w, data, digest)
	default:
		return errors.New("unsupported byte frame format")
	}
}

// Hex, standard Base64 and the digest contain no JSON metacharacters. Stream
// them into one text message with the same field order and trailing newline as
// json.Encoder. The scratch buffer belongs to this frame and is never pooled.
func writeByteFrameJSON(w io.Writer, data []byte, digest [sha256.Size]byte) error {
	const (
		maxBuffer  = 32 << 10
		fixedBytes = len(`{"hex":"","base64":"","hash":""}`) + 1 + 2*sha256.Size
	)

	// A small frame fits in one write. Large samples use bounded scratch.
	size := min(maxBuffer, hex.EncodedLen(len(data))+base64.StdEncoding.EncodedLen(len(data))+fixedBytes)
	out := bufio.NewWriterSize(w, size)

	// bufio.Writer retains the first write error until Flush.
	_, _ = out.WriteString(`{"hex":"`)
	if _, err := hex.NewEncoder(out).Write(data); err != nil {
		return err
	}

	_, _ = out.WriteString(`","base64":"`)
	encoder := base64.NewEncoder(base64.StdEncoding, out)

	if _, err := encoder.Write(data); err != nil {
		return err
	}

	if err := encoder.Close(); err != nil {
		return err
	}

	_, _ = out.WriteString(`","hash":"`)
	_, _ = out.WriteString(hex.EncodeToString(digest[:]))
	_, _ = out.WriteString("\"}\n")

	return out.Flush()
}
