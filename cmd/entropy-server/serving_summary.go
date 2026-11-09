package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

func observeConnection(ctx context.Context, _ net.Conn) context.Context {
	metrics.AddTCPConnection()

	return ctx
}

// Counts accepted writes without buffering a second copy of a large WS frame.
type payloadWriter struct {
	io.Writer

	written uint64
}

func (w *payloadWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	w.written += uint64(n)

	return n, err
}

func writeObservedJSON(conn *websocket.Conn, value any) error {
	return writeObservedPayload(conn, websocket.TextMessage, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(value)
	})
}

func writeObservedPayload(conn *websocket.Conn, kind int, encode func(io.Writer) error) error {
	writer, err := conn.NextWriter(kind)
	if err != nil {
		return err
	}

	counted := &payloadWriter{Writer: writer}
	err = encode(counted)
	closeErr := writer.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	// Partial/failed messages are deliberately excluded from completed payloads.
	metrics.AddWSPayloadBytes(counted.written)

	return nil
}

func logServingSummary(logger *log.Logger, uptime time.Duration, drained bool, totals metrics.ServingTotals) {
	state := "complete"
	if !drained {
		state = "incomplete_snapshot"
	}

	const megabyte = 1e6 // Decimal MB, consistent across HTTP and WS.

	logger.Printf("shutdown summary uptime=%s drain=%s served_MB=%.3f HTTP_body_MB=%.3f WS_payload_MB=%.3f",
		uptime.Round(time.Millisecond), state, float64(totals.HTTPBodyBytes)/megabyte+float64(totals.WSPayloadBytes)/megabyte,
		float64(totals.HTTPBodyBytes)/megabyte, float64(totals.WSPayloadBytes)/megabyte)
	logger.Printf("shutdown connections TCP_accepted=%d HTTP_requests=%d WS_sessions=%d WS_messages=%d (HTTP requests include upgrades)",
		totals.TCPConnections, totals.HTTPRequests, totals.WSSessions, totals.WSMessages)
	logger.Printf("shutdown DRBG_generated_MB=%.3f (includes internal seed work; served bytes exclude transport framing and are not peer acknowledgements)",
		float64(totals.GeneratedBytes)/megabyte)
}
