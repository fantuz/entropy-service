package client

import (
	"context"
	"time"

	"github.com/gorilla/websocket"

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
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	if err != nil {
		return nil, err
	}

	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})

	conn.SetReadLimit(1 << 25) // 32 MB
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	// conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		// panic("critical read deadline reached")
		return nil
	})

	// out := make([]byte, 1<<16)
	// out := make(chan []byte, 1<<25) // 16 is safe
	out := make(chan StreamFrame, 16)

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
