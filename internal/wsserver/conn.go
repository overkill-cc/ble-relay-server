package wsserver

import (
	"context"
	"log"

	"nhooyr.io/websocket"
)

// wsConn adapts a nhooyr.io/websocket connection to the session.Conn
// interface: Send enqueues onto a buffered channel drained by a single
// writer goroutine, so callers (including code holding a session lock)
// never block on network I/O.
type wsConn struct {
	id      string
	ws      *websocket.Conn
	outbox  chan []byte
	closeCh chan struct{}
}

func newWSConn(id string, ws *websocket.Conn) *wsConn {
	c := &wsConn{
		id:      id,
		ws:      ws,
		outbox:  make(chan []byte, 64),
		closeCh: make(chan struct{}),
	}
	go c.writePump()
	return c
}

func (c *wsConn) writePump() {
	ctx := context.Background()
	for {
		select {
		case msg, ok := <-c.outbox:
			if !ok {
				return
			}
			if err := c.ws.Write(ctx, websocket.MessageText, msg); err != nil {
				log.Printf("wsserver: write error for %s: %v", c.id, err)
				return
			}
		case <-c.closeCh:
			return
		}
	}
}

// Send implements session.Conn.
func (c *wsConn) Send(frame []byte) {
	select {
	case c.outbox <- frame:
	default:
		// Outbox full: the peer isn't keeping up. Drop rather than block the
		// broker/registry; the app-level protocol is request/response with
		// timeouts, so a dropped frame surfaces as a client-side timeout
		// rather than silently corrupting state.
		log.Printf("wsserver: outbox full for %s, dropping frame", c.id)
	}
}

// Close implements session.Conn.
func (c *wsConn) Close(reason string) {
	select {
	case <-c.closeCh:
		return
	default:
		close(c.closeCh)
	}
	_ = c.ws.Close(websocket.StatusNormalClosure, reason)
}
