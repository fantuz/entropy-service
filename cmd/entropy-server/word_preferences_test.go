package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWordLanguagePreferenceValidation(t *testing.T) {
	for _, tc := range []struct {
		name, data, query, want string
		enabled                 bool
		invalid                 bool
	}{
		{name: "old numeric schema", data: `{"bytes":64,"words":5}`, want: "en", enabled: true},
		{name: "canonical", data: `{"lang":"ru"}`, want: "ru", enabled: true},
		{name: "alias", data: `{"lang":"zh-TW"}`, want: "zh-Hant", enabled: true},
		{name: "query wins", data: `{"lang":"ru"}`, query: "lang=zh-CN", want: "zh-Hans", enabled: true},
		{name: "unknown stored language", data: `{"lang":"unknown"}`, want: "en", enabled: true},
		{name: "empty stored language", data: `{"lang":""}`, want: "en", enabled: true},
		{name: "control in stored language", data: `{"lang":"ru\u0000"}`, want: "en", enabled: true},
		{name: "wrong stored type", data: `{"lang":3}`, want: "en", enabled: true},
		{name: "malformed JSON", data: `{`, want: "en", enabled: true},
		{name: "oversized", data: strings.Repeat("x", 4097), want: "en", enabled: true},
		{name: "disabled", data: `{"lang":"ru"}`, want: "en"},
		{name: "disabled explicit query", data: `{"lang":"ru"}`, query: "lang=af", want: "af"},
		{name: "invalid query does not use cookie", data: `{"lang":"ru"}`, query: "lang=unknown", enabled: true, invalid: true},
		{name: "empty query does not use cookie", data: `{"lang":"ru"}`, query: "lang=", enabled: true, invalid: true},
		{name: "duplicate query", data: `{"lang":"ru"}`, query: "lang=af&lang=af", enabled: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/words?"+tc.query, nil)
			request.AddCookie(&http.Cookie{
				Name: preferenceCookieName, Value: base64.StdEncoding.EncodeToString([]byte(tc.data)),
				Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})

			dictionary, err := preferredWordList(request, tc.enabled)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid explicit query accepted")
				}

				return
			}

			if err != nil || dictionary.Info().Code != tc.want {
				t.Fatalf("language = %v, %v; want %s", dictionary, err, tc.want)
			}
		})
	}
}

func TestWordLanguagePreferenceHTTPSRoundTrip(t *testing.T) {
	for _, transport := range []string{"https", "tls-proxy"} {
		t.Run(transport, func(t *testing.T) {
			client, endpoint := preferenceTestServer(t, transport, true)
			_, cookies := preferenceGet(t, client, endpoint+"/v1/meta/random?lang=zh-TW&bytes=64&words=5&refresh=700")
			assertPreferenceCookie(t, cookies)

			body, _ := preferenceGet(t, client, endpoint+"/v1/meta/random")
			if !strings.Contains(string(body), `lang="zh-Hant"`) {
				t.Fatal("HTTP word page did not remember canonical language")
			}

			catalog, _ := preferenceGet(t, client, endpoint+"/wordlists.json")

			var metadata struct {
				Default  string `json:"default"`
				Selected string `json:"selected"`
			}
			if err := json.Unmarshal(catalog, &metadata); err != nil || metadata.Default != "en" || metadata.Selected != "zh-Hant" {
				t.Fatalf("browser catalog lost cookie language: %s, %v", catalog, err)
			}

			httpTransport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatal("test client must use the local test TLS transport")
			}

			dialer := websocket.Dialer{TLSClientConfig: httpTransport.TLSClientConfig, Jar: client.Jar}
			streamURL := "wss" + strings.TrimPrefix(endpoint, "https") + "/words?lang=ru&words=2&refresh=1"

			conn, response, err := dialer.DialContext(t.Context(), streamURL, nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if err != nil {
				t.Fatal(err)
			}

			defer conn.Close()

			assertPreferenceCookie(t, response.Cookies())

			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

			var frame EntropyFrame
			if err := conn.ReadJSON(&frame); err != nil || frame.Language != "ru" || len(strings.Fields(frame.Words)) != 2 {
				t.Fatalf("wrong WSS language/count: %+v, %v", frame, err)
			}

			inherited, response, err := dialer.DialContext(t.Context(), "wss"+strings.TrimPrefix(endpoint, "https")+"/words?refresh=1", nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if err != nil {
				t.Fatal(err)
			}

			defer inherited.Close()

			_ = inherited.SetReadDeadline(time.Now().Add(3 * time.Second))
			if err := inherited.ReadJSON(&frame); err != nil || frame.Language != "ru" || len(strings.Fields(frame.Words)) != 8 {
				t.Fatalf("missing lang must inherit the cookie; WS count keeps its server default: %+v, %v", frame, err)
			}

			body, _ = preferenceGet(t, client, endpoint+"/paroleparoleparole")
			if !strings.Contains(string(body), `lang="ru"`) {
				t.Fatal("WSS upgrade did not save the language for HTTP word pages")
			}

			body, _ = preferenceGet(t, client, endpoint+"/v1/data/random")
			if len(body) != 64 {
				t.Fatal("saving language discarded remembered byte count")
			}
		})
	}
}

func TestWordLanguageCatalogPreferenceIsPrivate(t *testing.T) {
	mux := http.NewServeMux()
	registerWordUI(mux, true)

	for _, code := range []string{"ar", "ru", "en"} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/wordlists.json", nil)

		cookie, err := newPrefsCookie(ClientPrefs{Lang: code})
		if err != nil {
			t.Fatal(err)
		}

		request.AddCookie(cookie)
		request.Header.Set("If-None-Match", `"stale-shared-catalog"`)

		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)

		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"selected":"`+code+`"`) || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Vary") != "Cookie" || len(response.Result().Cookies()) != 0 {
			t.Fatalf("personalized catalog must be private and read-only: %d %v", response.Code, response.Header())
		}
	}
}

func TestWordLanguageRejectedUpgradeDoesNotSetCookie(t *testing.T) {
	for _, query := range []string{"lang=ru", "lang=invalid", "lang=af&words=0", "lang=ar&lang=ru"} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/words?"+query, nil)
		response := httptest.NewRecorder()
		wsWordsHandler(wordTestDRBG(t), 8, 1, 1024, true, newWebSocketBudget(8, 1<<20))(response, request)

		if response.Code != http.StatusBadRequest || len(response.Result().Cookies()) != 0 {
			t.Fatal("failed upgrade persisted preferences", query, response.Code)
		}
	}
}
