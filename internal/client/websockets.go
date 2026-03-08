package client

import (
	"context"
	"github.com/gorilla/websocket"
)

/*
func StreamEntropy(url string) (<-chan []byte, error) {

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}

	out := make(chan []byte)

	go func() {

		defer close(out)
		defer conn.Close()

		for {

			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			out <- msg
		}
	}()

	return out, nil
}

*/

func StreamEntropy(ctx context.Context, url string, quantity int) (<-chan []byte, error) {

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}

	conn.SetReadLimit(4 << 20)
	
	out := make(chan []byte, 32) // 16 is safe

	go func() {

		defer close(out)
		defer conn.Close()

		for {

			select {

			case <-ctx.Done():
				return

			default:

				_, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}

				select {

				case out <- msg:

				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}
