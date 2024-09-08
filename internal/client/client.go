package client

import (
	"bufio"
	"crypto/tls"
	"log"
	"net"
	"net/http"
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
		go c.httpRequestToWebSocket2(utils.GzipDecode(data))
	}
}

func (c *Client) httpRequestToWebSocket(data []byte) {
	requestId, request, err := utils.ResumeRequest2(data, c.config.Local)
	if err != nil {
		return
	}
	err = utils.ClientRequest(requestId, c.conn, request)
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
			_ = utils.WriteToConnect(0xff000004, requestId, []byte(err.Error()), c.conn)
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
	err = utils.ClientResponse(requestId, c.conn, resp)
	if err != nil {
		log.Println(err)
		return
	}
	isSuccess = true

	/*

		// 接收响应
		reader := bufio.NewReader(conn)
		// 读取 并解析 响应头
		var headers bytes.Buffer
		var contentLength int64
		var transferEncoding string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Println("Error reading response headers:", err)
				return
			}
			// 如果 line 以小写字母开头，则将其转换为大写符合标准的 HTTP 头（注意不要影响 冒号右侧的内容）
			if line[0] >= 'a' && line[0] <= 'z' {
				fields := strings.SplitN(line, ":", 2)
				if len(fields) == 2 {
					fields[0] = http.CanonicalHeaderKey(fields[0])
					line = strings.Join(fields, ":") + "\n"
				}
			}

			if line == "\r\n" {
				headers.WriteString(line)
				break
			} else if strings.HasPrefix(line, "Content-Length:") {
				contentLength, _ = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")), 10, 64)
				headers.WriteString(line)
			} else if strings.HasPrefix(line, "Transfer-Encoding:") {
				transferEncoding = strings.TrimSpace(strings.TrimPrefix(line, "Transfer-Encoding:"))
				headers.WriteString(line)
			} else if strings.HasPrefix(line, "Connection:") {
				if strings.Contains(strings.ToLower(line), "keep-alive") {
					// 替换为 connection: close
					headers.WriteString("Connection: close\r\n")
				} else {
					headers.WriteString(line)
				}
			} else if strings.HasPrefix(line, "Host:") {
				headers.WriteString("Host: 127.0.0.1\r\n")
			} else {
				headers.WriteString(line)
			}
		}
		// 将 Http 头发送到 WebSocket
		if contentLength == 0 && transferEncoding == "" {
			// 没有正文
			err := utils.WriteToConnect(0xff000003, requestId, []byte(headers.String()), c.conn)
			if err != nil {
				return
			}
		} else {
			err := utils.WriteToConnect(0xff000002, requestId, []byte(headers.String()), c.conn)
			if err != nil {
				return
			}
		}

		// 读取响应体
		if contentLength > 0 && contentLength < 10240 {
			var body bytes.Buffer
			_, err = io.CopyN(&body, reader, contentLength)
			if err != nil {
				fmt.Println("Error reading response body:", err)
				return
			}
			// 将响应体发送到 WebSocket
			err := utils.WriteToConnect(0xff000002, requestId, body.Bytes(), c.conn)
			if err != nil {
				return
			}
		} else {
			// 读取很大的响应体或者 transferEncoding == "chunked" 的情况
			for {
				// 每次读取 2048 个字节
				var buf [2048]byte
				n, err := reader.Read(buf[:])
				if err != nil {
					fmt.Println("Error reading response body:", err)
					return
				}
				// 对于 transferEncoding == "chunked" 的情况，需要进行解析 0\r\n\r\n 作为结束标记。

				// 将响应体发送到 WebSocket
				err = utils.WriteToConnect(0xff000002, requestId, buf[:n], c.conn)
				if err != nil {
					return
				}

			}
		}
	*/
}
