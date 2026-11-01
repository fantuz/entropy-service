package main

import (
	"crypto/sha256"
	"encoding/base64"
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
	byteFrameHash    byteFrameFormat = "hash"
)

func parseByteFrameFormat(query url.Values) (byteFrameFormat, error) {
	values, present := query["format"]
	if !present {
		return byteFrameJSON, nil
	}

	if len(values) == 1 {
		switch format := byteFrameFormat(values[0]); format {
		case byteFrameJSON, byteFrameCompact, byteFrameHash:
			return format, nil
		}
	}

	return "", errors.New("format must be supplied once as json, compact or hash")
}

func writeByteFrame(conn *websocket.Conn, data []byte, format byteFrameFormat) error {
	kind := websocket.TextMessage
	if format == byteFrameCompact {
		kind = websocket.BinaryMessage
	}

	writer, err := conn.NextWriter(kind)
	if err != nil {
		return err
	}

	err = encodeByteFrame(writer, data, format)
	closeErr := writer.Close()

	if err != nil {
		return err
	}

	return closeErr
}

func encodeByteFrame(w io.Writer, data []byte, format byteFrameFormat) error {
	digest := sha256.Sum256(data)

	switch format {
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
		return json.NewEncoder(w).Encode(EntropyDataFrame{
			Hex: hex.EncodeToString(data), Base64: base64.StdEncoding.EncodeToString(data),
			Hash: hex.EncodeToString(digest[:]),
		})
	default:
		return errors.New("unsupported byte frame format")
	}
}
