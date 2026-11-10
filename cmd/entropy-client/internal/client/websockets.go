package client

import (
	"context"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/metrics"
)

// StreamFrame carries timing with its payload. ReceiveWait covers a complete
// message read, including waiting for the server; it is not network latency.
// Call Release after the last use of Data. Data must not be used after Release.
// Callers that retain frames without releasing them still receive owned buffers,
// but forfeit reuse. Copies share the same idempotent release operation.
type StreamFrame struct {
	Data         []byte
	ReceiveWait  time.Duration
	CycleRuntime time.Duration // HTTP request pacing plus fetch; zero for WS.
	lease        *frameLease
}

func (f StreamFrame) Release() {
	if f.lease != nil {
		f.lease.release()
	}
}

func StreamEntropy(ctx context.Context, endpoint string) (<-chan StreamFrame, error) {
	conn, size, err := openEntropyStream(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamFrame, 1)
	go func() {
		defer close(out)

		_ = receiveStream(ctx, conn, size, func(frame StreamFrame) error {
			select {
			case out <- frame:
				return nil
			case <-ctx.Done():
				frame.Release()
				return ctx.Err()
			}
		})
	}()

	return out, nil
}

// ReceiveEntropy synchronously delivers complete frames and returns the actual
// disconnect cause. consume owns each delivered lease, including on error.
// A caller can use one producer/queue across reconnects without adding buffers.
func ReceiveEntropy(ctx context.Context, endpoint string, consume func(StreamFrame) error) error {
	conn, size, err := openEntropyStream(ctx, endpoint)
	if err != nil {
		return err
	}

	return receiveStream(ctx, conn, size, consume)
}

func openEntropyStream(ctx context.Context, endpoint string) (*websocket.Conn, int, error) {
	size, err := streamFrameSize(endpoint)
	if err != nil {
		return nil, 0, err
	}

	conn, err := dialWebSocket(ctx, endpoint)
	if err != nil {
		return nil, 0, err
	}

	return conn, size, nil
}

func receiveStream(ctx context.Context, conn *websocket.Conn, size int, consume func(StreamFrame) error) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()

	conn.SetReadLimit(maxEntropyBytes)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		return nil
	})

	// One queued frame, one reader and one consumer. Only the consumer returns
	// its buffer; the producer never overwrites a queued or retained payload.
	pool := make(chan []byte, 3)

	for ctx.Err() == nil {
		started := time.Now()
		frame, err := readStreamFrame(conn, pool, size)
		frame.ReceiveWait = time.Since(started)

		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			return &streamReadError{cause: err}
		}

		if diag.ClientRateMeter != nil {
			diag.ClientRateMeter.Update(len(frame.Data))
		}

		metrics.AddClientBytesReceived(uint64(len(frame.Data)))
		metrics.IncClientWSSMessages()

		if err := consume(frame); err != nil {
			return err
		}
	}

	return ctx.Err()
}
