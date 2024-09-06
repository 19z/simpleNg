package client

import (
	"log"
	"net/http"
	"simpleNg/pkg/config"
	"simpleNg/pkg/utils"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	config *config.ClientConfig
	conn   *websocket.Conn
}

func NewClient(cfg *config.ClientConfig) (*Client, error) {
	client := &Client{
		config: cfg,
	}

	err := client.connect()
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (c *Client) connect() error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	conn, _, err := dialer.Dial(c.config.Domain, nil)
	if err != nil {
		return err
	}

	c.conn = conn
	return nil
}

func (c *Client) HandleRequest(w http.ResponseWriter, r *http.Request) {
	if c.conn == nil {
		http.Error(w, "WebSocket connection not established", http.StatusInternalServerError)
		return
	}

	// Forward the request to the local service
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send the response back to the server
	err = utils.CopyResponse(c.conn, resp)
	if err != nil {
		log.Printf("Failed to send response: %v", err)
	}
}
