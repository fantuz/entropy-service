package pipe

import (
	"io"
	"net/http"
	"os"
)

func WriteStdout(data []byte) error {

	_, err := os.Stdout.Write(data)
	return err
}

func pipe(server string) error {

	for {

		resp, err := http.Get(server)
		if err != nil {
			return err
		}

		_, err = io.Copy(os.Stdout, resp.Body)

		resp.Body.Close()

		if err != nil {
			return err
		}
	}
}
