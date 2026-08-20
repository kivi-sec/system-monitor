// Package websocket implements a small, dependency-light broadcast hub on
// top of gorilla/websocket. The design goals are: never block the
// collector's publish call, never leak goroutines when a client
// disconnects, and degrade gracefully (drop a slow client) rather than
// let one bad connection stall everyone else.
package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 1024 // clients only ever send tiny control frames
	sendBufferSize = 8    // small buffer; a slow client drops frames, not the hub
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// Local-only tool: same-origin by default. CheckOrigin is permissive
	// here because the server binds to 127.0.0.1 unless explicitly
	// reconfigured - see README "Security notes" for exposing it further.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Hub tracks connected clients and fans out broadcast messages to each of
// them via a per-client buffered channel and write goroutine.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
}

// NewHub creates an empty Hub ready to accept connections and broadcasts.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
	}
}

// ClientCount returns the number of currently connected websocket clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Broadcast serializes v to JSON once and pushes it to every connected
// client's send buffer. If a client's buffer is full (i.e. it's too slow
// to keep up), that client is dropped instead of blocking the broadcast
// for everyone else.
func (h *Hub) Broadcast(v interface{}) {
	payload, err := json.Marshal(v)
	if err != nil {
		log.Printf("websocket: failed to marshal broadcast payload: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		select {
		case c.send <- payload:
		default:
			// Buffer full - this client is too slow. Close it
			// asynchronously; its readPump/writePump will clean up and
			// unregister themselves. We never block here.
			go c.close()
		}
	}
}

// ServeWS upgrades an HTTP connection to a websocket and registers the
// resulting client with the hub. It returns once the connection has been
// handed off; the client's own goroutines manage its lifecycle from here.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	c := &client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, sendBufferSize),
	}

	h.register(c)

	// Two goroutines per client, both of which exit and are cleaned up
	// as soon as the connection closes for any reason - no leaks.
	go c.writePump()
	go c.readPump()

	return nil
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}
