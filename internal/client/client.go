package client

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	"simpleNg/internal/tunnel"
	"simpleNg/pkg/config"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

type Client struct {
	config  *config.ClientConfig
	session *yamux.Session
}

func NewClient(cfg *config.ClientConfig) (*Client, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout:  5 * time.Second,
		Subprotocols:      []string{tunnel.Subprotocol},
		EnableCompression: true,
	}
	urls := []string{cfg.Domain}
	if !strings.HasPrefix(cfg.Domain, "ws://") && !strings.HasPrefix(cfg.Domain, "wss://") {
		urls = []string{"wss://" + cfg.Domain, "ws://" + cfg.Domain}
	}
	var lastErr error
	for _, address := range urls {
		target, err := url.Parse(address)
		if err != nil {
			return nil, err
		}
		target.Path, target.RawPath, target.RawQuery, target.Fragment = tunnel.Path, "", "", ""
		conn, resp, err := dialer.Dial(target.String(), nil)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err != nil {
			lastErr = err
			continue
		}
		if conn.Subprotocol() != tunnel.Subprotocol {
			conn.Close()
			return nil, fmt.Errorf("server does not support %s; upgrade both server and client", tunnel.Subprotocol)
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		kind, ready, err := conn.ReadMessage()
		conn.SetReadDeadline(time.Time{})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("tunnel registration failed: %w", err)
		}
		if kind != websocket.BinaryMessage || string(ready) != tunnel.Ready {
			conn.Close()
			return nil, fmt.Errorf("invalid tunnel registration response")
		}
		session, err := yamux.Server(tunnel.NewConn(conn), tunnel.Config())
		if err != nil {
			conn.Close()
			return nil, err
		}
		return &Client{config: cfg, session: session}, nil
	}
	return nil, lastErr
}

// Close releases the tunnel and all active forwarded connections.
func (c *Client) Close() error {
	return c.session.Close()
}

func (c *Client) MessageHandler() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer c.Close()
	for {
		stream, err := c.session.AcceptStream()
		if err != nil {
			return err
		}
		go c.forward(ctx, stream)
	}
}

func (c *Client) forward(ctx context.Context, stream *yamux.Stream) {
	defer stream.Close()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	local, err := dialer.DialContext(ctx, "tcp", c.config.Local)
	if err != nil {
		log.Printf("Failed to connect to local service: %v", err)
		// Return a valid HTTP error even when the service is unavailable.
		body := "Local service unavailable"
		fmt.Fprintf(stream, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		return
	}
	defer local.Close()

	// Each stream carries the original HTTP exchange, including upgraded
	// WebSocket frames. Never parse application frames as tunnel messages.
	done := make(chan struct{})
	go func() {
		io.Copy(local, stream)
		local.Close()
		close(done)
	}()
	io.Copy(stream, local)
	local.Close()
	stream.Close()
	stream.SetDeadline(time.Now())
	<-done
}
