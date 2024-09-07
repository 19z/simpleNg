package server

import (
	"bufio"
	"bytes"
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
			return nil, err
		case <-ticker.C:
			conn, ok = s.conns[host]
			if ok {
				return conn, nil
			}
		}
	}
	return conn, err
}

type serverRequestContext struct {
	requestId    uint32
	conn         *websocket.Conn
	req          *http.Request
	writer       *http.ResponseWriter
	isHeaderSend bool
	closeSignal  chan bool
}

var serverRequestContexts = make(map[uint32]*serverRequestContext)

func (s *Server) HandleRequest(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	// is websocket request?
	if websocket.IsWebSocketUpgrade(r) {
		s.HandleWebSocket(w, r)
		return
	}

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
		http.Error(w, "Failed to forward request:"+r.URL.String(), http.StatusInternalServerError)
		return
	}
	serverRequestContexts[requestId] = &serverRequestContext{
		requestId:   requestId,
		conn:        conn,
		req:         r,
		writer:      &w,
		closeSignal: make(chan bool),
	}

	// Wait for the response from the client
	// 这里结束函数，就直接返回了？？要如何处理，让它一直等待，直到客户端关闭或者在服务端其他地方主动关闭连接

	select {
	case <-serverRequestContexts[requestId].closeSignal:
		// Client closed the connection
		log.Printf("http request end for request %d", requestId)
		delete(serverRequestContexts, requestId)
	case <-time.After(time.Minute * 3):
		// Timeout after 3 minute
		log.Printf("Timeout for request %d", requestId)
		delete(serverRequestContexts, requestId)
	}
}

var socketMessages = make(chan []byte, 1024)       // 用于接收来自客户端的请求结果消息
var closedHosts = make(chan *websocket.Conn, 1024) // 用于通知客户端连接已关闭

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
	if oldConn, ok := s.conns[host]; ok {
		_ = oldConn.Close()
		closedHosts <- oldConn
	}
	conn.SetCloseHandler(func(code int, text string) error {
		closedHosts <- conn
		return nil
	})
	s.conns[host] = conn

	log.Printf("WebSocket connection established for host: %s", host)

	// Handle incoming messages from the client
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Failed to read message: %v", err)
			if oldConn, ok := s.conns[host]; ok {
				if oldConn == conn {
					delete(s.conns, host)
				}
			}
			_ = conn.Close()

			break
		}
		log.Printf("Received message from client: %v, %s", message[0:8], string(message[8:100]))
		socketMessages <- message
	}
}

func (ctx *serverRequestContext) Close() {
	if !ctx.isHeaderSend {
		(*ctx.writer).WriteHeader(http.StatusGatewayTimeout)
	}
	_, _ = (*ctx.writer).Write([]byte("Connection failed"))
	ctx.closeSignal <- true
}

// MessageHandler 处理消息
// 从队列中读取数据并回复给http请求的客户端。
// 同时从 socketMessages、closedHosts、一个定时器中读取数据。
func (s *Server) MessageHandler() {
	var ticker = time.NewTicker(time.Second * 10)
	for {
		select {
		case message := <-socketMessages:
			prefix, requestId, body, err := utils.ParseMessage(message)
			if err != nil {
				log.Printf("Failed to parse message: %v", err)
				continue
			}

			ctx, ok := serverRequestContexts[requestId]
			if !ok {
				log.Printf("Failed to find request context for requestId: %d", requestId)
				continue
			}

			switch prefix {
			case 0xff000002:
				// 处理中间数据块
				err := writeResponse(ctx, body)
				if err != nil {
					log.Printf("Failed to write response body: %v", err)
					continue
				}
			case 0xff000003:
				// 正文结束
				err := writeResponse(ctx, body)
				ctx.closeSignal <- true
				if err != nil {
					log.Printf("Failed to write response body: %v", err)
					continue
				}
			case 0xff000004:
				// 连接失败
				ctx.Close()
			default:
				log.Printf("Invalid message prefix: %d", prefix)
			}

		case conn := <-closedHosts:
			for _, ctx := range serverRequestContexts {
				if ctx.conn == conn {
					ctx.Close()
				}
			}
			_ = conn.Close()

		case <-ticker.C:
			// 检查是否有超时的请求
			now := time.Now()
			for _, ctx := range serverRequestContexts {
				if ctx.req.Context().Err() != nil {
					ctx.Close()
				} else {
					deadline, ok := ctx.req.Context().Deadline()
					if ok && now.After(deadline) {
						ctx.Close()
					}
				}
			}
		}
	}
}

// writeResponse 写入响应
// 这里的 body 会包含 http 的响应头内容。需要分离出来，以便使用 http.ResponseWriter 响应
func writeResponse(ctx *serverRequestContext, body []byte) error {
	w := *ctx.writer
	if ctx.isHeaderSend {
		// 既然已经发送过头部，就不需要再处理了，直接作为正文发送即可
		_, err := w.Write(body)
		if err != nil {
			return err
		}
		return nil
	}
	// 从 body 中分离 相应头和正文内容
	headerEndIndex := bytes.Index(body, []byte("\r\n\r\n"))
	if headerEndIndex == -1 {
		return fmt.Errorf("No header end in body")
	}
	header := body[:headerEndIndex+4]
	body = body[headerEndIndex+4:]
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), ctx.req)
	if err != nil {
		return err
	}
	// 将 response 的 header 复制到 w
	for k, v := range response.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	ctx.isHeaderSend = true
	w.WriteHeader(response.StatusCode)
	if len(body) == 0 {
		return nil
	}
	// 将 body 写入 w
	_, err = w.Write(body)
	if err != nil {
		return err
	}
	return nil
}
