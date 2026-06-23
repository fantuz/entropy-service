package client

import (
	"context"
	"time"
	//"strconv"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/metrics"
	"github.com/gorilla/websocket"
)

//var ClientRateMeter = diag.NewRateMeter()

func StreamEntropy(ctx context.Context, url string) (<-chan []byte, error) {

	//url = fmt.Sprintf("%s%sbytes=%d", url, sep, quantity)
	//var wsurl = url + "?bytes=" + strconv.Itoa(quantity) + "&refresh=" + fps
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}

	conn.SetReadLimit(1 << 25) // 32 MB
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	//conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		//panic("critical read deadline reached")
		return nil
	})

	//out := make([]byte, 1<<16)
	out := make(chan []byte, 1<<25) // 16 is safe

	go func() {

		defer close(out)
		defer conn.Close()

		for {

			select {
			case <-ctx.Done():
				return
			default:
			}

			//conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(time.Second))

			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			if diag.ClientRateMeter != nil {
				diag.ClientRateMeter.Update(len(msg))
			}

			//diag.ClientRateMeter.Update(len(msg))
			// instrumentation: increment counters on successful read
			metrics.AddClientBytesReceived(uint64(len(msg)))
			metrics.IncClientWSSMessages() // optional counter for messages received

			// push into channel (non-blocking if buffered)
			select {
			case out <- msg:
			case <-ctx.Done():
				panic("critical end of ws context reached")
			}
		}
	}()

	return out, nil
}
