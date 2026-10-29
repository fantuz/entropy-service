package main

import (
	"net/http"
	"sync"
)

const httpRequestMemory int64 = 64 << 10

// Reservations bound admitted generation work, not process RSS. Encoder
// scratch, GC, TLS and kernel buffers can retain additional memory.
type httpRequestBudget struct {
	mu                                             sync.Mutex
	maxRequests, active                            int
	maxMemory, reserved, wordMemory                int64
	rejectedRequests, rejectedMemory, rejectedSize uint64
}

type httpBudgetUsage struct {
	Active, MaxRequests                            int
	Reserved, MaxMemory                            int64
	RejectedRequests, RejectedMemory, RejectedSize uint64
}

func newHTTPRequestBudget(maxRequests int, maxMemory int64) *httpRequestBudget {
	longestWord := 0
	for _, word := range dicewareWords {
		longestWord = max(longestWord, len(word))
	}

	return &httpRequestBudget{
		maxRequests: maxRequests, maxMemory: maxMemory,
		wordMemory: 128 + 16*(int64(longestWord)+1),
	}
}

func httpReservation(quantity int, perItem int64) int64 {
	if quantity < 1 || perItem < 1 || int64(quantity) > ((1<<63-1)-httpRequestMemory)/perItem {
		return 1<<63 - 1
	}

	return httpRequestMemory + int64(quantity)*perItem
}

func (b *httpRequestBudget) admit(w http.ResponseWriter, r *http.Request, needed int64) (func(), bool) {
	if r.Context().Err() != nil {
		http.Error(w, "request canceled", http.StatusServiceUnavailable)

		return nil, false
	}

	release, code, message := b.tryReserve(needed)
	if code != 0 {
		if code == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "1")
		}

		http.Error(w, message, code)

		return nil, false
	}

	return release, true
}

func (b *httpRequestBudget) tryReserve(needed int64) (func(), int, string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if needed <= 0 || needed > b.maxMemory {
		b.rejectedSize++

		return nil, http.StatusRequestEntityTooLarge, "HTTP request exceeds configured generation memory budget"
	}

	if b.active >= b.maxRequests {
		b.rejectedRequests++

		return nil, http.StatusServiceUnavailable, "HTTP generation request budget exhausted"
	}

	if needed > b.maxMemory-b.reserved {
		b.rejectedMemory++

		return nil, http.StatusServiceUnavailable, "HTTP generation memory budget exhausted"
	}

	b.active++
	b.reserved += needed

	var once sync.Once

	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.active--
			b.reserved -= needed
			b.mu.Unlock()
		})
	}, 0, ""
}

// Fixed-size image, QR and sound routes reserve before rendering. Byte and
// word routes admit inside their handlers, after validating the chosen size.
func (b *httpRequestBudget) wrap(next http.HandlerFunc, needed int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		release, ok := b.admit(w, r, needed)
		if !ok {
			return
		}
		defer release()

		next(w, r)
	}
}

func (b *httpRequestBudget) usage() httpBudgetUsage {
	b.mu.Lock()
	defer b.mu.Unlock()

	return httpBudgetUsage{
		Active: b.active, MaxRequests: b.maxRequests,
		Reserved: b.reserved, MaxMemory: b.maxMemory,
		RejectedRequests: b.rejectedRequests, RejectedMemory: b.rejectedMemory,
		RejectedSize: b.rejectedSize,
	}
}
