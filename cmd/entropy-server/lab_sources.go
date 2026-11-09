package main

import (
	"bytes"
	"context"
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
			if name != "source" && name != "bytes" && name != labRefreshQuery && name != "preview" {
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

		camera := source.Width > 0
		preview := false

		if values, present := query["preview"]; present {
			if !camera || len(values) != 1 || (values[0] != "0" && values[0] != "1") {
				http.Error(w, "preview requires a camera and must be 0 or 1", http.StatusBadRequest)
				return
			}

			preview = values[0] == "1"
			if preview && source.Width*source.Height*3 > maxBytes {
				http.Error(w, "preview frame exceeds server byte limit", http.StatusBadRequest)
				return
			}
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

		reservation := wsReservation(count, 2) + (128 << 10)
		if camera {
			reservation = wsReservation(source.Width*source.Height*3, 2) + (16 << 20)
		}

		release, admitted := budget.admit(w, r, reservation)
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

		credentials := lab.Credentials{}

		if camera {
			stop := context.AfterFunc(r.Context(), func() { _ = conn.Close() })

			conn.SetReadLimit(8192)
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			kind, payload, readErr := conn.ReadMessage()

			stop()

			decoder := json.NewDecoder(bytes.NewReader(payload))
			decoder.DisallowUnknownFields()

			if readErr != nil || kind != websocket.TextMessage || len(bytes.TrimSpace(payload)) == 0 || bytes.TrimSpace(payload)[0] != '{' || decoder.Decode(&credentials) != nil || decoder.Decode(new(any)) != io.EOF || len(credentials.Username) > 256 || len(credentials.Password) > 1024 {
				labClose(conn, websocket.ClosePolicyViolation, "Camera requires one credentials object")
				return
			}

			_ = conn.SetReadDeadline(time.Time{})
		}

		ctx, cleanup := websocketContext(r.Context(), conn)
		defer cleanup()

		upstream, err := registry.OpenAuthenticated(ctx, source.ID, count, refresh, credentials)
		if err != nil {
			reason := "Registered source unavailable"
			if camera {
				reason = "Camera unavailable; check registration, credentials and FFmpeg"
			}

			labClose(conn, websocket.CloseTryAgainLater, reason)

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
				readCount := count
				if preview {
					readCount = source.Width * source.Height * 3
				}

				data, readErr := upstream.Read(ctx, readCount)
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
						reason := "Source read failed or timed out"

						var statusError *lab.HTTPStatusError
						if errors.As(readErr, &statusError) {
							reason = "HTTP source returned status " + strconv.Itoa(statusError.Code)
						}

						if camera {
							reason = "Camera failed; check credentials, stream and FFmpeg"
						}

						labClose(conn, websocket.CloseInternalServerErr, reason)
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
