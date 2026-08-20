package websocket

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// client wraps a single websocket connection. Metrics only ever flow
// server -> client, but we still run a readPump so we can respond to
// pings/pongs and detect a dead connection promptly.
type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	closeOnce sync.Once
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		c.hub.unregister(c)
		_ = c.conn.Close()
	})
}

// readPump keeps the connection alive (pong handling) and exits - closing
// the connection - as soon as the client disconnects or misbehaves.
func (c *client) readPump() {
	defer c.close()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		// We don't expect meaningful application messages from the
		// client, just discard whatever arrives; ReadMessage also
		// surfaces close frames and I/O errors, which is what actually
		// terminates this loop.
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// writePump drains the client's send channel to the socket and sends
// periodic pings. It exits (and closes the connection) when the channel
// is closed by the hub or a write fails.
func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel - send a clean close frame.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
