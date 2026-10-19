// Package tunnel carries independent TCP streams over a WebSocket connection.
package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

const Subprotocol = "simple-ng-tunnel-v1"
const Ready = "simple-ng-ready"
const Path = "/__simple_ng_internal__/tunnel/v1"

// Conn adapts binary WebSocket messages to yamux's ordered byte stream.
// WebSocket control messages continue to be handled by gorilla/websocket.
type Conn struct {
	ws      *websocket.Conn
	reader  io.Reader
	writeMu sync.Mutex
}

func NewConn(ws *websocket.Conn) *Conn {
	return &Conn{ws: ws}
}

func (c *Conn) Read(p []byte) (int, error) {
	for {
		if c.reader == nil {
			kind, reader, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				return 0, fmt.Errorf("unexpected tunnel message type: %d", kind)
			}
			c.reader = reader
		}
		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *Conn) Close() error         { return c.ws.Close() }
func (c *Conn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func Config() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.StreamOpenTimeout = 10 * time.Second
	return cfg
}

// Open respects cancellation even if the session's stream backlog is full.
func Open(ctx context.Context, session *yamux.Session) (net.Conn, error) {
	type result struct {
		stream net.Conn
		err    error
	}
	opened := make(chan result)
	go func() {
		stream, err := session.Open()
		select {
		case opened <- result{stream, err}:
		case <-ctx.Done():
			if stream != nil {
				stream.Close()
			}
		}
	}()
	select {
	case result := <-opened:
		return result.stream, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
