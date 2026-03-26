package client

import (
	"context"
	"fmt"
	//"strings"
	"github.com/gorilla/websocket"
	"github.com/fantuz/entropy-service/entropy-client/internal/stats"
)

func TestEntropyWS(ctx context.Context, endpoint string) error {

	//refwsurl := "ws://127.0.0.1:8080/stream?bytes=2097152"
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	tester := stats.NewStreamTester()

	fmt.Println("Starting WebSocket entropy streaming test")

	lastReport := uint64(0)

	for {

		select {

		case <-ctx.Done():
			//fmt.Println("Finished WebSocket entropy streaming test")
			return nil

		default:

			msgType, data, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			
			if msgType != websocket.BinaryMessage {
				continue
			}

			tester.Add(data)

			if tester.Total-lastReport >= 1<<20 {

				res := tester.Result()
				//var text = strings.ReplaceAll(res, " ", "	")
				reportStats("WS", res)

				lastReport = tester.Total
			}
		}
	}
}

