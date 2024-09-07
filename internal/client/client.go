package client

import (
	"crypto/tls"
	"simpleNg/pkg/config"
	"simpleNg/pkg/utils"
	"strings"
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
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
	}

	var url string
	var err error
	var conn *websocket.Conn
	if strings.HasPrefix(c.config.Domain, "ws://") || strings.HasPrefix(c.config.Domain, "wss://") {
		url = c.config.Domain
	} else {
		for _, protocol := range []string{"ws://", "wss://"} {
			url = protocol + c.config.Domain
			conn, _, err = dialer.Dial(url, nil)
			if err == nil {
				c.conn = conn
				break
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) MessageHandler() error {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return err
		}
		go c.httpRequestToWebSocket(data)
	}
}

func (c *Client) httpRequestToWebSocket(data []byte) {
	requestId, request, err := utils.ResumeRequest(data, c.config.Port)
	if err != nil {
		return
	}
	err = utils.ClientRequest(requestId, c.conn, request)
}
