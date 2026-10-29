package client

import (
	"context"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/metrics"
)

// StreamFrame carries timing with the matching payload, even when frames queue.
// ReceiveWait measures one complete ReadMessage call, including waiting for the
// server to send. It is not a round-trip or one-way network latency measurement.
type StreamFrame struct {
	Data        []byte
	ReceiveWait time.Duration
}

func StreamEntropy(ctx context.Context, url string) (<-chan StreamFrame, error) {
	// url = fmt.Sprintf("%s%sbytes=%d", url, sep, quantity)
	// var wsurl = url + "?bytes=" + strconv.Itoa(quantity) + "&refresh=" + fps
	// conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	conn, err := dialWebSocket(ctx, url)
	if err != nil {
		return nil, err
	}

	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})

	conn.SetReadLimit(maxEntropyBytes)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	// conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		// panic("critical read deadline reached")
		return nil
	})

	// One queued frame bounds backlog while preserving ordered delivery and
	// backpressure. The reader and consumer can each also hold a live frame.
	out := make(chan StreamFrame, 1)

	go func() {
		defer close(out)
		defer conn.Close()
		defer stop()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))

			readStarted := time.Now()
			_, msg, err := conn.ReadMessage()
			receiveWait := time.Since(readStarted)

			if err != nil {
				return
			}

			if diag.ClientRateMeter != nil {
				diag.ClientRateMeter.Update(len(msg))
			}

			// diag.ClientRateMeter.Update(len(msg))
			// instrumentation: increment counters on successful read
			metrics.AddClientBytesReceived(uint64(len(msg)))
			metrics.IncClientWSSMessages() // optional counter for messages received

			// push into channel (non-blocking if buffered)
			// Wait for queue space or cancellation
			select {
			case out <- StreamFrame{Data: msg, ReceiveWait: receiveWait}:
			case <-ctx.Done():
				// panic("critical end of ws context reached")
				return
			}
		}
	}()

	return out, nil
}
