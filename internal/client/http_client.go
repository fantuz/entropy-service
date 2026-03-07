package client

import (
	"io"
	"net/http"
)

func FetchEntropy(url string, size* int) ([]byte, error) {

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	return data, err
	//return io.ReadAll(resp.Body)
}

/*
func fetchEntropy() ([]byte, error) {

	resp, err := http.Get(serverURL)

	data, err := io.ReadAll(resp.Body)

	return data, err
}
*/
