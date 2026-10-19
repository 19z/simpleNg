package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"simpleNg/internal/tunnel"
	"simpleNg/pkg/config"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

type websocketForwardContext struct {
	requestId   uint32
	localConn   *websocket.Conn
	closeSignal chan bool
}

type Client struct {
	config  *config.ClientConfig
	session *yamux.Session
	wire    *tunnel.Conn
	files   http.Handler
}

// ErrTakenOver distinguishes domain replacement from a retryable disconnect.
var ErrTakenOver = errors.New("tunnel was taken over by another client")

func NewClient(cfg *config.ClientConfig) (*Client, error) {
	return newClient(context.Background(), cfg)
}

func newClient(ctx context.Context, cfg *config.ClientConfig) (*Client, error) {
	var files http.Handler
	var err error
	if strings.HasPrefix(strings.ToLower(cfg.Local), "file:") {
		files, err = NewDirectoryHandler(cfg.Local)
		if err != nil {
			return nil, err
		}
	}
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
		conn, resp, err := dialer.DialContext(ctx, target.String(), nil)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
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
		wire := tunnel.NewConn(conn)
		session, err := yamux.Server(wire, tunnel.Config())
		if err != nil {
			conn.Close()
			return nil, err
		}
		return &Client{config: cfg, session: session, wire: wire, files: files}, nil
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
	if c.files != nil {
		server := &http.Server{Handler: c.files, ReadHeaderTimeout: 10 * time.Second}
		defer server.Close()
		err := server.Serve(&directoryListener{Session: c.session, addr: c.wire.LocalAddr()})
		return c.connectionError(err)
	}
	for {
		stream, err := c.session.AcceptStream()
		if err != nil {
			return c.connectionError(err)
		}
		go c.forward(ctx, stream)
	}
}

type directoryListener struct {
	*yamux.Session
	addr net.Addr
}

func (l *directoryListener) Addr() net.Addr { return l.addr }

func (c *Client) connectionError(err error) error {
	code, _ := c.wire.CloseReason()
	if code == tunnel.CloseCodeTakenOver {
		return ErrTakenOver
	}
	return err
}

// RunWithReconnect retries network failures with capped exponential backoff.
// A takeover is terminal: retrying it would evict the newer domain owner.
func RunWithReconnect(ctx context.Context, cfg *config.ClientConfig) error {
	if strings.HasPrefix(strings.ToLower(cfg.Local), "file:") {
		if _, err := NewDirectoryHandler(cfg.Local); err != nil {
			return err
		}
	}
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		conn, err := newClient(ctx, cfg)
		if err == nil {
			started := time.Now()
			log.Printf("Client connected to %s", cfg.Domain)
			done := make(chan error, 1)
			go func() { done <- conn.MessageHandler() }()
			select {
			case err = <-done:
			case <-ctx.Done():
				conn.Close()
				<-done
				return nil
			}
			conn.Close()
			if errors.Is(err, ErrTakenOver) {
				log.Printf("Client for %s was taken over by another client; stopping", cfg.Domain)
				return nil
			}
			if time.Since(started) >= time.Minute {
				backoff = time.Second
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("Tunnel to %s disconnected: %v; reconnecting in %s", cfg.Domain, err, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
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
