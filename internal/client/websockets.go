package client

import (
	"github.com/gorilla/websocket"
)

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
