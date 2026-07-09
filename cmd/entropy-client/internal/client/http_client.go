package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/metrics"
)

// "encoding/json"
// "net/http/cookiejar"
// "net/url"
// "os"

// Original simple fetch helper, kept for reference:
// func fetchEntropy(endpoint *string, quantity *int) ([]byte, error) {
// 	resp, err := http.Get(*endpoint)
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	defer resp.Body.Close()
//
// 	data, err := io.ReadAll(resp.Body)
//
// 	return data, err
// }

func FetchEntropySimple(endpoint *string, quantity *int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return FetchEntropy(ctx, *endpoint, *quantity)
}

// FetchEntropy requests `quantity` bytes from `endpoint` and returns the raw bytes.
// The function takes a context so caller can cancel / set a deadline.
func FetchEntropy(ctx context.Context, endpoint string, quantity int64) ([]byte, error) {
	/*
		url := endpoint
		if quantity > 0 && !containsBytesParam(endpoint) {
			//fmt.Println("PRE-FETCH-ENTROPY", url)
			sep := "?"
			if hasQuery(endpoint) {
				sep = "&"
			}
			url = fmt.Sprintf("%s%sbytes=%d", endpoint, sep, quantity)
			//fmt.Println("POST-FETCH-ENTROPY", url)
			//time.Sleep(500 * time.Millisecond)
		}
	*/

	// var cookies []*http.Cookie

	// create instance
	// jar, _ := cookiejar.New(nil)

	// read on startup
	// file, _ := os.ReadFile("cookies.json")

	// parse
	// _ = json.Unmarshal(file, &cookies)

	// u, _ := url.Parse(endpoint)
	// jar.SetCookies(u, cookies)

	// store
	// object, err := json.Marshal(cookies)
	// if err == nil {
	// 	_ = os.WriteFile("cookies.json", object, 0o600)
	// }

	// parsedc := jar.Cookies(u)
	// fmt.Printf(" --> %q\n", jar)
	// fmt.Printf(" --> %q\n", parsedc)
	// fmt.Printf(" --> %q\n", cookies)
	// fmt.Printf(" --> %q\n", u)
	// fmt.Printf(" --> %q\n", object)

	// Create a client with a conservative timeout bound (per request).
	// Caller can still use ctx to cancel earlier.
	httpClient := &http.Client{
		// Jar:     jar,
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			IdleConnTimeout:     60 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
			// TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// fmt.Println("ENTROPY from", endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))

		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	// consider streaming/chunking.
	// Read fully (for small samples). If you need streaming, replace with io.Copy and counting writer.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	metrics.AddClientBytesReceived(uint64(len(data)))

	// diag.ClientRateMeter.Update(len(data))
	if diag.ClientRateMeter != nil {
		diag.ClientRateMeter.Update(len(data))
	}

	return data, nil
}

// small helpers to avoid clobbering the endpoint if caller gave "/entropy?bytes=..."
// Kept for reference:
// func containsBytesParam(u string) bool {
// 	return (len(u) >= 6 && ( // trivial check for "bytes="
// 	// quick substring search
// 	// avoid importing strings for tiny helper; but using strings is ok — keep it simple:
// 	func() bool {
// 		for i := 0; i+6 <= len(u); i++ {
// 			if u[i:i+6] == "bytes=" {
// 				return true
// 			}
// 		}
// 		return false
// 	}()))
// }
//
// func hasQuery(u string) bool {
// 	for i := 0; i < len(u); i++ {
// 		if u[i] == '?' {
// 			return true
// 		}
// 	}
// 	return false
// }
