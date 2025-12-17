package server

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
	"simpleNg/pkg/config"
	"simpleNg/pkg/utils"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type connection struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

type Server struct {
	config *config.ServerConfig
	conns  map[string]*connection
}

func NewServer(cfg *config.ServerConfig) (*Server, error) {
	return &Server{
		config: cfg,
		conns:  make(map[string]*connection),
	}, nil
}

// getConnectWithDomain returns a WebSocket connection associated with the given
// host. If the connection is not established within {trySeconds} seconds, it returns an
// error.
func (s *Server) getConnectWithDomain(host string, trySeconds int) (*connection, error) {
	var conn *connection
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
	conn         *connection
	req          *http.Request
	writer       *http.ResponseWriter
	isHeaderSend bool
	closeSignal  chan bool
}

var serverRequestContexts = sync.Map{} //make(map[uint32]*serverRequestContext)

func (s *Server) HandleRequest(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	// is websocket request?
	if websocket.IsWebSocketUpgrade(r) {
		// 检查是否是内部协议连接（使用特定路径标识）
		if r.URL.Path == "/__simpleNg_internal__" {
			s.HandleWebSocket(w, r)
			return
		}
		// 普通 WebSocket 请求，需要转发
		s.HandleWebSocketForward(w, r)
		return
	}

	conn, err := s.getConnectWithDomain(host, 10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// Forward the request to the client
	requestId := utils.GetNextRequestId()

	err = s.CopyRequest(requestId, conn, r)
	if err != nil {
		log.Printf("Failed to forward request: %v", err)
		http.Error(w, "Failed to forward request:"+r.URL.String(), http.StatusInternalServerError)
		return
	}
	_context := &serverRequestContext{
		requestId:   requestId,
		conn:        conn,
		req:         r,
		writer:      &w,
		closeSignal: make(chan bool),
	}
	serverRequestContexts.Store(requestId, _context)

	// Wait for the response from the client
	// 这里结束函数，就直接返回了？？要如何处理，让它一直等待，直到客户端关闭或者在服务端其他地方主动关闭连接

	select {
	case <-_context.closeSignal:
		// Client closed the connection
		log.Printf("http request end for request %d", requestId)
		//delete(serverRequestContexts, requestId)
		serverRequestContexts.Delete(requestId)
	case <-time.After(time.Minute * 3):
		// Timeout after 3 minute
		log.Printf("Timeout for request %d", requestId)
		//delete(serverRequestContexts, requestId)
		serverRequestContexts.Delete(requestId)
	}
}

var socketMessages = make(chan []byte, 1024)   // 用于接收来自客户端的请求结果消息
var closedHosts = make(chan *connection, 1024) // 用于通知客户端连接已关闭

// WebSocket 转发相关的上下文
type websocketForwardContext struct {
	requestId   uint32
	conn        *connection
	clientConn  *websocket.Conn
	closeSignal chan bool
}

var websocketForwardContexts = sync.Map{}              // map[uint32]*websocketForwardContext
var websocketForwardMessages = make(chan []byte, 1024) // 用于接收来自客户端的 WebSocket 转发消息

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
		_ = oldConn.conn.Close()
		closedHosts <- oldConn
	}
	s.conns[host] = &connection{conn: conn}
	conn.SetCloseHandler(func(code int, text string) error {
		closedHosts <- s.conns[host]
		return nil
	})

	log.Printf("WebSocket connection established for host: %s", host)

	// Handle incoming messages from the client
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Failed to read message: %v", err)
			if oldConn, ok := s.conns[host]; ok {
				if oldConn.conn == conn {
					delete(s.conns, host)
				}
			}
			_ = conn.Close()

			break
		}
		message = utils.GzipDecode(message)
		log.Printf("Received message from client: %v %d %s", message[0:8], len(message), string(message[8:100]))
		socketMessages <- message
	}
}

// HandleWebSocketForward 处理需要转发的普通 WebSocket 请求
func (s *Server) HandleWebSocketForward(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	conn, err := s.getConnectWithDomain(host, 10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// 升级到 WebSocket
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade WebSocket connection: %v", err)
		return
	}

	requestId := utils.GetNextRequestId()
	ctx := &websocketForwardContext{
		requestId:   requestId,
		conn:        conn,
		clientConn:  clientConn,
		closeSignal: make(chan bool),
	}
	websocketForwardContexts.Store(requestId, ctx)

	log.Printf("WebSocket forward connection established for host: %s, requestId: %d", host, requestId)

	// 转发 WebSocket 升级请求到客户端
	err = s.CopyWebSocketRequest(requestId, conn, r)
	if err != nil {
		log.Printf("Failed to forward WebSocket request: %v", err)
		_ = clientConn.Close()
		websocketForwardContexts.Delete(requestId)
		return
	}

	// 从客户端 WebSocket 读取数据并转发到内部连接
	go func() {
		defer func() {
			_ = clientConn.Close()
			websocketForwardContexts.Delete(requestId)
		}()

		for {
			messageType, message, err := clientConn.ReadMessage()
			if err != nil {
				log.Printf("Failed to read from client WebSocket: %v", err)
				// 通知客户端关闭连接
				_ = s.CopyWebSocketData(requestId, conn, messageType, nil, true)
				break
			}
			// 转发数据到客户端
			err = s.CopyWebSocketData(requestId, conn, messageType, message, false)
			if err != nil {
				log.Printf("Failed to forward WebSocket data: %v", err)
				break
			}
		}
	}()

	// 等待关闭信号
	select {
	case <-ctx.closeSignal:
		log.Printf("WebSocket forward connection closed for requestId: %d", requestId)
		_ = clientConn.Close()
	case <-time.After(time.Minute * 10):
		log.Printf("WebSocket forward connection timeout for requestId: %d", requestId)
		_ = clientConn.Close()
	}
}

// CopyRequest 服务器端
// 这个函数的作用是将一个HTTP请求写入到一个WebSocket连接中。它假设请求没有被修改过，并且返回一个错误如果写入或读取请求失败。
func (s *Server) CopyRequest(requestId uint32, conn *connection, req *http.Request) error {
	var requestBuf bytes.Buffer
	err := req.Write(&requestBuf)
	if err != nil {
		return err
	}
	prefix := []byte{0xff, 0x00, 0x00, 0x00}
	prefix = append(prefix, make([]byte, 4)...)
	binary.LittleEndian.PutUint32(prefix[4:], requestId)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	err = conn.conn.WriteMessage(websocket.BinaryMessage, utils.GzipEncode(append(prefix, requestBuf.Bytes()...)))
	if err != nil {
		return err
	}
	return nil
}

// CopyWebSocketRequest 转发 WebSocket 升级请求到客户端
func (s *Server) CopyWebSocketRequest(requestId uint32, conn *connection, req *http.Request) error {
	var requestBuf bytes.Buffer
	err := req.Write(&requestBuf)
	if err != nil {
		return err
	}
	// 使用 0xff000010 作为 WebSocket 请求的前缀
	prefix := []byte{0xff, 0x00, 0x00, 0x10}
	prefix = append(prefix, make([]byte, 4)...)
	binary.BigEndian.PutUint32(prefix[4:], requestId)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	err = conn.conn.WriteMessage(websocket.BinaryMessage, utils.GzipEncode(append(prefix, requestBuf.Bytes()...)))
	if err != nil {
		return err
	}
	return nil
}

// CopyWebSocketData 转发 WebSocket 数据到客户端
func (s *Server) CopyWebSocketData(requestId uint32, conn *connection, messageType int, data []byte, isClose bool) error {
	// 构建消息：前缀(4字节) + requestId(4字节) + messageType(4字节) + 数据
	msg := make([]byte, 12)
	if isClose {
		binary.BigEndian.PutUint32(msg, 0xff000011) // WebSocket 关闭
	} else {
		binary.BigEndian.PutUint32(msg, 0xff000010) // WebSocket 数据
	}
	binary.BigEndian.PutUint32(msg[4:], requestId)
	binary.BigEndian.PutUint32(msg[8:], uint32(messageType))
	if data != nil {
		msg = append(msg, data...)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	err := conn.conn.WriteMessage(websocket.BinaryMessage, utils.GzipEncode(msg))
	if err != nil {
		return err
	}
	return nil
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

			// 检查是否是 WebSocket 转发消息
			if prefix == 0xff000010 || prefix == 0xff000011 {
				_wsCtx, ok := websocketForwardContexts.Load(requestId)
				if !ok {
					log.Printf("Failed to find WebSocket forward context for requestId: %d", requestId)
					continue
				}
				wsCtx := _wsCtx.(*websocketForwardContext)

				if prefix == 0xff000011 {
					// WebSocket 关闭
					_ = wsCtx.clientConn.Close()
					wsCtx.closeSignal <- true
					websocketForwardContexts.Delete(requestId)
				} else {
					// WebSocket 数据转发
					if len(body) >= 4 {
						messageType := int(binary.BigEndian.Uint32(body[:4]))
						data := body[4:]
						err := wsCtx.clientConn.WriteMessage(messageType, data)
						if err != nil {
							log.Printf("Failed to write WebSocket message: %v", err)
							_ = wsCtx.clientConn.Close()
							wsCtx.closeSignal <- true
							websocketForwardContexts.Delete(requestId)
						}
					}
				}
				continue
			}

			// 处理普通 HTTP 请求响应
			_ctx, ok := serverRequestContexts.Load(requestId)
			if !ok {
				log.Printf("Failed to find request context for requestId: %d", requestId)
				continue
			}
			var ctx *serverRequestContext = _ctx.(*serverRequestContext)

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
			serverRequestContexts.Range(func(key, value interface{}) bool {
				if ctx, ok := value.(*serverRequestContext); ok && ctx.conn == conn {
					ctx.Close()
				}
				return true
			})
			_ = conn.conn.Close()

		case <-ticker.C:
			// 检查是否有超时的请求
			now := time.Now()
			serverRequestContexts.Range(func(key, value interface{}) bool {
				ctx := value.(*serverRequestContext)
				if ctx.req.Context().Err() != nil {
					ctx.Close()
				} else {
					deadline, ok := ctx.req.Context().Deadline()
					if ok && now.After(deadline) {
						ctx.Close()
					}
				}
				return true
			})
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
