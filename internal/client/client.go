package client

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"simpleNg/pkg/config"
	"simpleNg/pkg/utils"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	config *config.ClientConfig
	conn   *websocket.Conn
	mu     sync.Mutex
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
		conn, _, err = dialer.Dial(url, nil)
		if err != nil {
			return err
		}
		c.conn = conn
	} else {
		for _, protocol := range []string{"wss://", "ws://"} {
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
		//go c.httpRequestToWebSocket(data)
		go c.httpRequestToWebSocket2(data)
	}
}

func (c *Client) httpRequestToWebSocket2(data []byte) {
	// 创建 TCP 连接
	requestId, requestData, err := utils.ResumeRequest3(data)
	if err != nil {
		log.Println(err)
		return
	}
	conn, err := net.Dial("tcp", c.config.Local)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()
	isSuccess := false
	defer func() {
		if !isSuccess {
			if err != nil {
				_ = c.WriteToConnect(0xff000004, requestId, []byte(err.Error()))
			} else {
				_ = c.WriteToConnect(0xff000004, requestId, []byte("timeout"))
			}
		}
	}()

	// 发送数据
	_, err = conn.Write(requestData)
	if err != nil {
		log.Println(err)
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		log.Println(err)
		return
	}
	err = c.ClientResponse(requestId, resp)
	if err != nil {
		log.Println(err)
		return
	}
	isSuccess = true
}

func (c *Client) ClientResponse(requestId uint32, resp *http.Response) error {
	// 读取响应头
	respBytes, err := httputil.DumpResponse(resp, false)
	if err != nil {
		return c.WriteToConnect(0xff000004, requestId, []byte(err.Error()))
	}
	// 读取响应内容并分块发送
	buffer := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buffer)
		if err != nil && err != io.EOF {
			// 读取失败，返回 0xff000004 + requestId
			//errCode := make([]byte, 8)
			//binary.BigEndian.PutUint32(errCode, 0xff000004)
			//binary.BigEndian.PutUint32(errCode[4:], requestId)
			//log.Printf("write: %v", errCode)
			//conn.WriteMessage(websocket.BinaryMessage, errCode)
			return c.WriteToConnect(0xff000004, requestId, nil)
		}

		if err == io.EOF {
			// 正文结束，发送 0xff000003 + requestId
			//endPrefix := make([]byte, 8)
			//binary.BigEndian.PutUint32(endPrefix, 0xff000003)
			//binary.BigEndian.PutUint32(endPrefix[4:], requestId)
			//log.Printf("write: %v %s", endPrefix, string(buffer[:n]))
			//conn.WriteMessage(websocket.BinaryMessage, endPrefix)
			if respBytes != nil {
				err = c.WriteToConnect(0xff000003, requestId, append(respBytes, buffer[:n]...))
				respBytes = nil
				return err
			}
			err = c.WriteToConnect(0xff000003, requestId, buffer[:n])
			if err != nil {
				return err
			}
			break
		} else if n > 0 {
			// 发送数据块
			//prefix := make([]byte, 8)
			//binary.BigEndian.PutUint32(prefix, 0xff000002)
			//binary.BigEndian.PutUint32(prefix[4:], requestId)
			//log.Printf("write: %v %s", prefix, string(buffer[:n]))
			//conn.WriteMessage(websocket.BinaryMessage, append(prefix, buffer[:n]...))
			if respBytes != nil {
				err = c.WriteToConnect(0xff000002, requestId, append(respBytes, buffer[:n]...))
				respBytes = nil
				if err != nil {
					return err
				}
			} else {
				err = c.WriteToConnect(0xff000002, requestId, buffer[:n])
				if err != nil {
					return err
				}
			}

		}
	}

	return nil
}

func (c *Client) WriteToConnect(prefix uint32, requestId uint32, body []byte) error {
	if body == nil {
		body = []byte{}
	}
	data := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(data, prefix)
	binary.BigEndian.PutUint32(data[4:], requestId)
	copy(data[8:], body)
	//zipData := GzipEncode(data)
	//log.Printf("write: %v %d %s", data[0:8], len(zipData), string(body[0:min(len(body), 100)]))
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.conn.WriteMessage(websocket.BinaryMessage, data)
	if err != nil {
		return err
	}
	return nil
}
