package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func assertPreferenceCookie(t *testing.T, cookies []*http.Cookie) {
	t.Helper()

	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want one preference cookie", len(cookies))
	}

	c := cookies[0]
	if c.Name != "entropy_prefs" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" || c.MaxAge != 86400 {
		t.Fatalf("unexpected preference-cookie policy: %+v", c)
	}
}

func TestPreferenceCookiePolicyAndContents(t *testing.T) {
	response := httptest.NewRecorder()

	prefs := ClientPrefs{Bytes: 64, Words: 5, Refresh: 700}
	if err := setPrefsCookie(response, prefs); err != nil {
		t.Fatal(err)
	}

	cookies := response.Result().Cookies()
	assertPreferenceCookie(t, cookies)

	raw, err := base64.StdEncoding.DecodeString(cookies[0].Value)
	if err != nil {
		t.Fatal(err)
	}

	var fields map[string]int
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}

	if len(fields) != 3 || fields["bytes"] != 64 || fields["words"] != 5 || fields["refresh"] != 700 {
		t.Fatalf("cookie must contain only the numeric preferences: %s", raw)
	}
}

func preferenceTestServer(t *testing.T, transport string, enabled bool) (*http.Client, string) {
	t.Helper()

	mux := http.NewServeMux()
	budget := newHTTPRequestBudget(8, 32<<20)
	mux.HandleFunc("/v1/data/random", randomBytesHandler(observableTestDRBG(t), 1024, 8, enabled, budget))
	mux.HandleFunc("/v1/meta/random", randomWordsHandler(new(wordTestSource), 8, 5000, 1024, enabled, budget))
	mux.HandleFunc("/paroleparoleparole", randomWordsHandler(new(wordTestSource), 8, 5000, 1024, enabled, budget))

	var server *httptest.Server

	switch transport {
	case "http":
		server = httptest.NewServer(mux)
	case "https":
		server = httptest.NewTLSServer(mux)
	default:
		backend := httptest.NewServer(mux)
		t.Cleanup(backend.Close)

		endpoint, err := url.Parse(backend.URL)
		if err != nil {
			t.Fatal(err)
		}

		server = httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(endpoint))
	}

	t.Cleanup(server.Close)
	client := server.Client()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	client.Jar, client.Timeout = jar, 3*time.Second

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	if transport == "http" {
		// Go and browsers can permit Secure cookies on loopback. Route a
		// reserved test hostname locally to exercise ordinary HTTP instead.
		address := endpoint.Host
		httpTransport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
		}}
		client.Transport = httpTransport
		t.Cleanup(httpTransport.CloseIdleConnections)

		endpoint.Host = net.JoinHostPort("entropy.example", endpoint.Port())
	}

	return client, endpoint.String()
}

func preferenceGet(t *testing.T, client *http.Client, endpoint string) ([]byte, []*http.Cookie) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Contradictory forwarded headers must never remove Secure, whether TLS is
	// direct or terminated at a proxy. The application does not trust them.
	request.Header.Set("X-Forwarded-Proto", "http")

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("preference request failed: status=%d error=%v", response.StatusCode, err)
	}

	return data, response.Cookies()
}

func TestPreferenceCookieRoundTrip(t *testing.T) {
	for _, transport := range []string{"http", "https", "tls-proxy"} {
		for _, enabled := range []bool{false, true} {
			t.Run(transport+"/enabled="+boolLabel(enabled), func(t *testing.T) {
				client, endpoint := preferenceTestServer(t, transport, enabled)

				data, cookies := preferenceGet(t, client, endpoint+"/v1/data/random?bytes=64&words=5&refresh=700")
				if len(data) != 64 {
					t.Fatal("explicit byte count did not take precedence")
				}

				if enabled {
					assertPreferenceCookie(t, cookies)
				} else if len(cookies) != 0 {
					t.Fatal("cookies were issued while disabled")
				}

				wantBytes, wantWords := 1024, 8
				if enabled {
					wantWords = 3
					if transport != "http" {
						wantBytes, wantWords = 64, 5
					}
				}

				data, _ = preferenceGet(t, client, endpoint+"/v1/data/random")
				if len(data) != wantBytes {
					t.Fatalf("remembered byte count = %d, want %d", len(data), wantBytes)
				}

				for _, path := range []string{"/v1/meta/random", "/paroleparoleparole"} {
					page, _ := preferenceGet(t, client, endpoint+path)

					words := regexp.MustCompile(`<div class="words">([^<]*)</div>`).FindSubmatch(page)
					if len(words) != 2 || len(strings.Fields(string(words[1]))) != wantWords {
						t.Fatalf("word preferences did not cross HTTP endpoints: %s", page)
					}

					if !bytes.Contains(page, []byte(`http-equiv="refresh" content="5"`)) {
						t.Fatal("stored refresh overrode the server's HTTP page interval")
					}
				}

				data, _ = preferenceGet(t, client, endpoint+"/v1/data/random?bytes=32")
				if len(data) != 32 {
					t.Fatal("query did not override a remembered byte count")
				}

				if enabled && transport != "http" {
					wantBytes = 32
				}

				data, _ = preferenceGet(t, client, endpoint+"/v1/data/random")
				if len(data) != wantBytes {
					t.Fatalf("updated byte preference = %d, want %d", len(data), wantBytes)
				}
			})
		}
	}
}

func TestExtractPrefsTreatsCookiesAsUntrusted(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte(`{"bytes":64,"words":5,"refresh":700}`))

	defaults := ClientPrefs{Bytes: 1024, Words: 3, Refresh: 500}
	for _, tc := range []struct {
		name, cookie, query string
		enabled             bool
		want                ClientPrefs
	}{
		{"missing", "", "", true, defaults},
		{"valid", valid, "", true, ClientPrefs{Bytes: 64, Words: 5, Refresh: 700}},
		{"disabled", valid, "", false, ClientPrefs{Bytes: 1024, Words: 8, Refresh: 500}},
		{"malformed base64", "not-base64!", "", true, defaults},
		{"malformed JSON", base64.StdEncoding.EncodeToString([]byte("{")), "", true, defaults},
		{"out of bounds", base64.StdEncoding.EncodeToString([]byte(`{"bytes":1025,"words":9,"refresh":-1}`)), "", true, defaults},
		{"partial", base64.StdEncoding.EncodeToString([]byte(`{"bytes":0,"words":5}`)), "", true, ClientPrefs{Bytes: 1024, Words: 5, Refresh: 500}},
		{"query overrides", valid, "?bytes=32&words=2&refresh=900", true, ClientPrefs{Bytes: 32, Words: 2, Refresh: 900}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/data/random"+tc.query, nil)
			if tc.cookie != "" {
				request.AddCookie(&http.Cookie{
					Name: preferenceCookieName, Value: tc.cookie,
					Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
				})
			}

			got, err := extractPrefs(request, 1024, 8, tc.enabled)
			if err != nil || got != tc.want {
				t.Fatalf("preferences = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestRejectedPreferenceRequestsDoNotSetCookies(t *testing.T) {
	d := observableTestDRBG(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, query string
		handler     http.HandlerFunc
		status      int
	}{
		{"invalid bytes", "?bytes=0", randomBytesHandler(d, 1024, 8, true, newHTTPRequestBudget(8, 32<<20)), http.StatusBadRequest},
		{"invalid words", "?words=9", randomWordsHandler(new(wordTestSource), 8, 5000, 1024, true, newHTTPRequestBudget(8, 32<<20)), http.StatusBadRequest},
		{"byte generation failed", "?bytes=64", randomBytesHandler(d, 1024, 8, true, newHTTPRequestBudget(8, 32<<20)), http.StatusInternalServerError},
		{"word generation failed", "", randomWordsHandler(bytes.NewReader(nil), 8, 5000, 1024, true, newHTTPRequestBudget(8, 32<<20)), http.StatusInternalServerError},
		{"admission rejected", "?bytes=64", randomBytesHandler(d, 1024, 8, true, newHTTPRequestBudget(1, 1)), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			tc.handler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+tc.query, nil))

			if response.Code != tc.status || len(response.Header().Values("Set-Cookie")) != 0 {
				t.Fatalf("rejected request persisted preferences: status=%d headers=%v", response.Code, response.Header())
			}
		})
	}
}
