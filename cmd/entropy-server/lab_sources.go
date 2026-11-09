package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/lab"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

const labRefreshQuery = "refresh"

func labAllowed(r *http.Request, allowRemote bool) bool {
	if allowRemote {
		return true
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)

	return err == nil && net.ParseIP(host).IsLoopback()
}

func labCatalogHandler(registry *lab.Registry, budget *webSocketBudget, maxBytes int, allowRemote bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "use GET", http.StatusMethodNotAllowed)

			return
		}

		usage := budget.usage()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Sources        []lab.Descriptor `json:"sources"`
			Allowed        bool             `json:"allowed"`
			MaxBytes       int              `json:"max_bytes"`
			MaxConnections int              `json:"max_connections"`
			Active         int              `json:"active"`
		}{Sources: registry.List(), Allowed: labAllowed(r, allowRemote), MaxBytes: maxBytes, MaxConnections: usage.MaxConnections, Active: usage.Active})
	}
}

func labStreamHandler(registry *lab.Registry, budget *webSocketBudget, maxBytes int, allowRemote bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		if !labAllowed(r, allowRemote) {
			http.Error(w, "lab bridge requires a loopback client or -lab-allow-remote", http.StatusForbidden)
			return
		}

		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, "invalid source query", http.StatusBadRequest)
			return
		}

		for name := range query {
			if name != "source" && name != "bytes" && name != labRefreshQuery {
				http.Error(w, "unknown source query parameter", http.StatusBadRequest)
				return
			}
		}

		if len(query["source"]) != 1 {
			http.Error(w, "source must be supplied once", http.StatusBadRequest)
			return
		}

		source, found := registry.Lookup(query.Get("source"))
		if !found {
			http.Error(w, "unknown registered source", http.StatusBadRequest)
			return
		}

		count, err := extractByteCount(r, min(65536, maxBytes, source.MaxBytes), min(maxBytes, source.MaxBytes))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		refresh := 500

		if values, ok := query[labRefreshQuery]; ok {
			if len(values) != 1 {
				http.Error(w, "refresh must be supplied once", http.StatusBadRequest)
				return
			}

			refresh, err = strconv.Atoi(values[0])
			if err != nil || refresh < 20 || refresh > 60000 {
				http.Error(w, "refresh must be 20–60000 milliseconds", http.StatusBadRequest)
				return
			}
		}

		release, admitted := budget.admit(w, r, wsReservation(count, 2)+(128<<10))
		if !admitted {
			return
		}
		defer release()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		metrics.AddWSSession()

		ctx, cleanup := websocketContext(r.Context(), conn)
		defer cleanup()

		upstream, err := registry.Open(ctx, source.ID, count, refresh)
		if err != nil {
			labClose(conn, websocket.CloseTryAgainLater, "Registered source unavailable")
			return
		}
		defer upstream.Close()

		ticker := time.NewTicker(time.Duration(refresh) * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				data, readErr := upstream.Read(ctx, count)
				if len(data) > 0 {
					_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
						return
					}

					metrics.AddWSPayloads(1)
					metrics.AddWSPayloadBytes(uint64(len(data)))
				}

				if readErr != nil {
					switch {
					case errors.Is(readErr, io.EOF):
						labClose(conn, websocket.CloseNormalClosure, "Source reached EOF")
					case errors.Is(readErr, lab.ErrTooLarge):
						labClose(conn, websocket.CloseMessageTooBig, "Source exceeds requested byte limit")
					default:
						labClose(conn, websocket.CloseInternalServerErr, "Source read failed or timed out")
					}

					if !errors.Is(readErr, io.EOF) && ctx.Err() == nil {
						log.Printf("lab source %q stopped after a read error", source.ID)
					}

					return
				}
			}
		}
	}
}

func labClose(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
}
