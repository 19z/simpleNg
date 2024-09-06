package server

import (
	"fmt"
	"log"
	"net/http"
	"simpleNg/pkg/config"
	"simpleNg/pkg/utils"
	"time"

	"github.com/gorilla/websocket"
)

type Server struct {
	config *config.ServerConfig
	conns  map[string]*websocket.Conn
}

func NewServer(cfg *config.ServerConfig) (*Server, error) {
	return &Server{
		config: cfg,
		conns:  make(map[string]*websocket.Conn),
	}, nil
}

// getConnectWithDomain returns a WebSocket connection associated with the given
// host. If the connection is not established within {trySeconds} seconds, it returns an
// error.
func (s *Server) getConnectWithDomain(host string, trySeconds int) (*websocket.Conn, error) {
	var conn *websocket.Conn
	var ok bool
	var err error

	// Wait at most 10 seconds for the connection to be established
	timeout := time.NewTimer(time.Second * time.Duration(trySeconds))
	defer timeout.Stop()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for !ok {
		select {
		case <-timeout.C:
			err = fmt.Errorf("No WebSocket connection found for this domain: %s", host)
		case <-ticker.C:
			conn, ok = s.conns[host]
		}
	}

	return conn, err
}

func (s *Server) HandleRequest(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	conn, err := s.getConnectWithDomain(host, 10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// Forward the request to the client
	requestId := utils.GetNextRequestId()
	err = utils.CopyRequest(requestId, conn, r)
	if err != nil {
		log.Printf("Failed to forward request: %v", err)
		http.Error(w, "Failed to forward request", http.StatusInternalServerError)
		return
	}

	// Wait for the response from the client
	resp, err := utils.ReadResponse(conn)
	if err != nil {
		log.Printf("Failed to read response: %v", err)
		http.Error(w, "Failed to read response", http.StatusInternalServerError)
		return
	}

	// Send the response back to the user
	utils.CopyResponse(w, resp)
}

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade WebSocket connection: %v", err)
		return
	}

	host := r.Host
	s.conns[host] = conn

	log.Printf("WebSocket connection established for host: %s", host)

	// Handle incoming messages from the client
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Failed to read message: %v", err)
			delete(s.conns, host)
			break
		}

		// Process the message (if needed)
		log.Printf("Received message from client: %s", message)
	}
}
