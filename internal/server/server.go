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
		log.Printf("Received message from client: %v %d %s", message[0:8], len(message), string(message[8:100]))
		socketMessages <- message
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
	err = conn.conn.WriteMessage(websocket.BinaryMessage, append(prefix, requestBuf.Bytes()...))
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
