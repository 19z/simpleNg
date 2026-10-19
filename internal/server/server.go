package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"simpleNg/internal/tunnel"
	"simpleNg/pkg/config"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

type connection struct {
	session   *yamux.Session
	proxy     *httputil.ReverseProxy
	transport *http.Transport
}

type Server struct {
	mu     sync.RWMutex
	conns  map[string]*connection
	closed bool
}

func NewServer(_ *config.ServerConfig) (*Server, error) {
	return &Server{conns: make(map[string]*connection)}, nil
}

func (s *Server) getConnectWithDomain(host string) (*connection, error) {
	s.mu.RLock()
	conn := s.conns[strings.ToLower(host)]
	s.mu.RUnlock()
	if conn == nil || conn.session.IsClosed() {
		return nil, fmt.Errorf("No WebSocket connection found for this domain: %s", host)
	}
	return conn, nil
}

func (s *Server) HandleRequest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == tunnel.Path {
		s.HandleWebSocket(w, r)
		return
	}
	conn, err := s.getConnectWithDomain(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	conn.proxy.ServeHTTP(w, r)
}

// HandleWebSocket registers a tunnel. Business WebSockets use HandleRequest's
// reverse proxy and cannot replace the tunnel for their domain.
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	supported := false
	for _, protocol := range websocket.Subprotocols(r) {
		if protocol == tunnel.Subprotocol {
			supported = true
			break
		}
	}
	if !websocket.IsWebSocketUpgrade(r) || !supported {
		http.Error(w, "This path requires a simpleNg tunnel WebSocket", http.StatusUpgradeRequired)
		return
	}
	upgrader := websocket.Upgrader{
		Subprotocols:      []string{tunnel.Subprotocol},
		EnableCompression: true,
		CheckOrigin:       func(*http.Request) bool { return true },
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade tunnel: %v", err)
		return
	}
	wire := tunnel.NewConn(ws)
	session, err := yamux.Client(wire, tunnel.Config())
	if err != nil {
		ws.Close()
		return
	}
	defer session.Close()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return tunnel.Open(ctx, session)
		},
		// Preserve upstream compression and stream response bodies as received.
		DisableCompression: true,
		IdleConnTimeout:    90 * time.Second,
	}
	defer transport.CloseIdleConnections()
	conn := &connection{session: session, transport: transport}
	conn.proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = req.Host
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Failed to forward request for %s: %v", r.Host, err)
			http.Error(w, "Failed to forward request", http.StatusBadGateway)
		},
	}

	host := strings.ToLower(r.Host)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	oldConn := s.conns[host]
	s.conns[host] = conn
	// The client returns from NewClient only after registration is visible.
	ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = ws.WriteMessage(websocket.BinaryMessage, []byte(tunnel.Ready))
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.conns[host] == conn {
			delete(s.conns, host)
		}
		s.mu.Unlock()
	}()
	if oldConn != nil {
		oldConn.session.Close()
		oldConn.transport.CloseIdleConnections()
	}
	if err != nil {
		session.Close()
		return
	}
	log.Printf("Tunnel established for host: %s", host)
	<-session.CloseChan()
}

// Close shuts down registered tunnels, including upgraded connections.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	conns := s.conns
	s.conns = make(map[string]*connection)
	s.mu.Unlock()
	for _, conn := range conns {
		conn.session.Close()
		conn.transport.CloseIdleConnections()
	}
	return nil
}
