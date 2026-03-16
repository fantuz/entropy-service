package client

import (
	"time"
	"context"
	"github.com/gorilla/websocket"
	"github.com/fantuz/entropy-service/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/entropy-client/internal/metrics"
)

//var ClientRateMeter = diag.NewRateMeter()

func StreamEntropy(ctx context.Context, url string, quantity int) (<-chan []byte, error) {

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}

	conn.SetReadLimit(4 << 20)
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))
	
	out := make(chan []byte, 32) // 16 is safe

	go func() {

		defer close(out)
		defer conn.Close()

		for {

			select {
			case <-ctx.Done():
				return
			default:
			}

				_, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}

				/*
				select {

				case out <- msg:

				case <-ctx.Done():
					return
				}
				*/
			//}

			//diag.ClientRateMeter.Update(len(msg))
			if diag.ClientRateMeter != nil {
				diag.ClientRateMeter.Update(len(msg))
			}

			// instrumentation: increment counters on successful read
			metrics.AddClientBytesReceived(uint64(len(msg)))
			metrics.IncClientWSSMessages() // optional counter for messages received

		//}

		// push into channel (non-blocking if buffered)
			select {
			case out <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
