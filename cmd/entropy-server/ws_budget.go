package main

import (
	"net/http"
	"sync"
)

const (
	wsInboundLimit           = 1024
	wsConnectionMemory int64 = 64 << 10
	// Raw data, hex/base64 strings and copies, JSON scratch, and growth headroom.
	wsJSONMemoryFactor int64 = 16
)

// This is admission accounting, not an RSS limit. Allow for live frame buffers,
// encoding copies and encoder growth; GC, pools, TLS and kernel buffers can
// retain additional memory after a reservation has been released.
type webSocketBudget struct {
	mu                  sync.Mutex
	maxConnections      int
	maxMemory           int64
	wordMemory          int64
	active              int
	reserved            int64
	rejectedConnections uint64
	rejectedMemory      uint64
	rejectedSize        uint64
}

type webSocketUsage struct {
	Active, MaxConnections                            int
	Reserved, MaxMemory                               int64
	RejectedConnections, RejectedMemory, RejectedSize uint64
}

func newWebSocketBudget(maxConnections int, maxMemory int64) *webSocketBudget {
	longestWord := 0
	for _, word := range dicewareWords {
		longestWord = max(longestWord, len(word))
	}

	return &webSocketBudget{
		maxConnections: maxConnections, maxMemory: maxMemory,
		// String slices, sampling temporaries, phrase copies and JSON escaping.
		wordMemory: 128 + 16*(int64(longestWord)+1),
	}
}

func wsByteReservation(quantity int, jsonFrame bool) int64 {
	factor := int64(1)
	if jsonFrame {
		factor = wsJSONMemoryFactor
	}

	return wsReservation(quantity, factor)
}

func wsReservation(quantity int, perItem int64) int64 {
	// In particular, -words can be as large as the platform's int. Saturate
	// instead of wrapping a large request into a small/negative reservation.
	if quantity < 1 || int64(quantity) > ((1<<63-1)-wsConnectionMemory)/perItem {
		return 1<<63 - 1
	}

	return wsConnectionMemory + int64(quantity)*perItem
}

func (b *webSocketBudget) admit(w http.ResponseWriter, r *http.Request, needed int64) (func(), bool) {
	if r.Context().Err() != nil {
		http.Error(w, "request canceled", http.StatusServiceUnavailable)

		return nil, false
	}

	code, message := 0, ""

	b.mu.Lock()

	switch {
	case needed <= 0 || needed > b.maxMemory:
		b.rejectedSize++
		code, message = http.StatusRequestEntityTooLarge, "WebSocket request exceeds configured memory budget; reduce output size"
	case b.active >= b.maxConnections:
		b.rejectedConnections++
		code, message = http.StatusServiceUnavailable, "WebSocket connection budget exhausted"
	case needed > b.maxMemory-b.reserved:
		b.rejectedMemory++
		code, message = http.StatusServiceUnavailable, "WebSocket memory budget exhausted"
	default:
		b.active++
		b.reserved += needed
	}

	b.mu.Unlock()

	if code != 0 {
		if code == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
		}

		http.Error(w, message, code)

		return nil, false
	}

	var once sync.Once

	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.active--
			b.reserved -= needed
			b.mu.Unlock()
		})
	}, true
}

func (b *webSocketBudget) usage() webSocketUsage {
	b.mu.Lock()
	defer b.mu.Unlock()

	return webSocketUsage{
		Active: b.active, MaxConnections: b.maxConnections,
		Reserved: b.reserved, MaxMemory: b.maxMemory,
		RejectedConnections: b.rejectedConnections,
		RejectedMemory:      b.rejectedMemory, RejectedSize: b.rejectedSize,
	}
}
