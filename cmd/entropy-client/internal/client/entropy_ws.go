package client

import (
	"context"
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/stats"
)

func TestEntropyWS(ctx context.Context, endpoint string) error {
	// refwsurl := "ws://127.0.0.1:8080/stream?bytes=2097152"
	conn, err := dialWebSocket(ctx, endpoint)
	if err != nil {
		return err
	}

	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	conn.SetReadLimit(maxEntropyBytes)

	tester := stats.NewStreamTester()

	fmt.Println("Starting WebSocket entropy streaming test")

	lastReport := uint64(0)

	for {
		select {
		case <-ctx.Done():
			// fmt.Println("Finished WebSocket entropy streaming test")
			return ctx.Err()

		default:
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}

				return err
			}

			if msgType != websocket.BinaryMessage {
				continue
			}

			tester.Add(data)

			if tester.Total-lastReport >= 1<<20 {
				res := tester.Result()
				// var text = strings.ReplaceAll(res, " ", "	")
				reportStats("WS", res)

				lastReport = tester.Total
			}
		}
	}
}
