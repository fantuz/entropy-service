package client

import (
	"context"
	"time"

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
	size, err := streamFrameSize(endpoint)
	if err != nil {
		return nil, err
	}

	conn, err := dialWebSocket(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })

	conn.SetReadLimit(maxEntropyBytes)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		return nil
	})

	// One queued frame, one reader and one consumer. Only the consumer returns
	// its buffer; the producer never overwrites a queued or retained payload.
	out := make(chan StreamFrame, 1)
	pool := make(chan []byte, 3)

	go func() {
		defer close(out)
		defer conn.Close()
		defer stop()

		for ctx.Err() == nil {
			started := time.Now()
			frame, err := readStreamFrame(conn, pool, size)
			frame.ReceiveWait = time.Since(started)

			if err != nil {
				return
			}

			if diag.ClientRateMeter != nil {
				diag.ClientRateMeter.Update(len(frame.Data))
			}

			metrics.AddClientBytesReceived(uint64(len(frame.Data)))
			metrics.IncClientWSSMessages()

			select {
			case out <- frame:
			case <-ctx.Done():
				frame.Release()

				return
			}
		}
	}()

	return out, nil
}
