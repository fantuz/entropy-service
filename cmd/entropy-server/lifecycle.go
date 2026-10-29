package main

import (
	"context"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// managedServer also waits for handlers on hijacked WebSocket connections.
type managedServer struct {
	*http.Server

	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	handlers sync.WaitGroup
	waitOnce sync.Once
	done     chan struct{}
}

func newManagedServer(parent context.Context, server *http.Server) *managedServer {
	ctx, cancel := context.WithCancel(parent)
	s := &managedServer{Server: server, cancel: cancel, done: make(chan struct{})}
	handler := server.Handler
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			http.Error(w, "server shutting down", http.StatusServiceUnavailable)

			return
		}

		s.handlers.Add(1)

		s.mu.Unlock()
		defer s.handlers.Done()

		// Pass the original writer through so WebSocket hijacking still works.
		handler.ServeHTTP(w, r)
	})

	return s
}

func (s *managedServer) Shutdown(ctx context.Context) error {
	// Close admission before waiting: no handler can Add after Wait starts.
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.cancel()

	err := s.Server.Shutdown(ctx)
	if err != nil {
		_ = s.Close()
	}

	s.waitOnce.Do(func() {
		go func() {
			s.handlers.Wait()
			close(s.done)
		}()
	})

	select {
	case <-s.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// websocketContext interrupts blocked reads AND writes on cancellation. Cleanup
// joins the reader before the handler releases its private generator.
func websocketContext(parent context.Context, conn *websocket.Conn) (context.Context, func()) {
	conn.SetReadLimit(wsInboundLimit)

	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer cancel()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	return ctx, func() {
		cancel()
		stop()

		_ = conn.Close()

		<-done
	}
}
