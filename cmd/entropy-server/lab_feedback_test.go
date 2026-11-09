package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/lab"
)

func TestLabUDPReadyEventPrecedesFirstDatagram(t *testing.T) {
	reserve, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	address := reserve.LocalAddr().String()
	_ = reserve.Close()

	registry, err := lab.New([]lab.Config{{ID: "udp", Kind: "udp", Endpoint: address}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	budget := newWebSocketBudget(1, 4<<20)

	server := httptest.NewServer(labStreamHandler(registry, budget, 4096, false))
	defer server.Close()

	conn := budgetConnect(t, server, "/lab/stream?source=udp&bytes=1024&refresh=20&events=1")
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))

	message, data, err := conn.ReadMessage()
	if err != nil || message != websocket.TextMessage || !strings.Contains(string(data), `"source-ready"`) || !strings.Contains(string(data), `"kind":"udp"`) {
		t.Fatalf("missing actual UDP bind acknowledgement: %q %v", data, err)
	}

	sender, err := (&net.Dialer{}).DialContext(t.Context(), "udp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	_, _ = sender.Write([]byte("one datagram"))

	message, data, err = conn.ReadMessage()
	if err != nil || message != websocket.BinaryMessage || string(data) != "one datagram" {
		t.Fatalf("lost first post-bind datagram: %q %v", data, err)
	}
}

func TestLabHTTPStatusEventsAndPrivacy(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/private-created-resource?token=fixture")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "created")
	}))
	defer peer.Close()

	registry, err := lab.New([]lab.Config{{ID: "page", Kind: "webpage", Endpoint: peer.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	server := httptest.NewServer(labStreamHandler(registry, newWebSocketBudget(1, 4<<20), 4096, false))
	defer server.Close()

	conn := budgetConnect(t, server, "/lab/stream?source=page&bytes=1024&refresh=20&events=1")
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	for _, expected := range []string{"source-ready", "http-status"} {
		kind, data, err := conn.ReadMessage()

		var value struct {
			Type     string `json:"type"`
			Status   int    `json:"status"`
			Location bool   `json:"location_present"`
		}
		if err != nil || kind != websocket.TextMessage || json.Unmarshal(data, &value) != nil || value.Type != expected {
			t.Fatalf("wrong metadata: %q %v", data, err)
		}

		if strings.Contains(string(data), "private-created") || strings.Contains(string(data), "token=") {
			t.Fatal("HTTP metadata disclosed a private URL")
		}

		if expected == "http-status" && (value.Status != 201 || !value.Location) {
			t.Fatal("missing HTTP 201/Location information")
		}
	}

	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(data) != "created" {
		t.Fatalf("201 body was not sampled: %q %v", data, err)
	}
}
