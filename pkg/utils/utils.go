package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"math/rand"
	"net/http"
)

// CopyRequest 服务器端
// 这个函数的作用是将一个HTTP请求写入到一个WebSocket连接中。它假设请求没有被修改过，并且返回一个错误如果写入或读取请求失败。
func CopyRequest(requestId uint32, conn *websocket.Conn, req *http.Request) error {
	// 定义前缀格式
	prefixFormat := "0xff00%04x%04x"
	// 初始化分块序号
	chunkSeq := 0
	// 读取请求内容
	buf := make([]byte, 4096)
	for {
		n, err := req.Body.Read(buf)
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
		// 拆分成小块
		chunks := split(buf[:n], 1024)
		// 对每个小块添加前缀
		for _, chunk := range chunks {
			prefix := fmt.Sprintf(prefixFormat, requestId, chunkSeq)
			chunkSeq++
			data := append([]byte(prefix), chunk...)
			err := conn.WriteMessage(websocket.TextMessage, data)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// 拆分成小块
func split(data []byte, size int) [][]byte {
	chunks := make([][]byte, 0, (len(data)+size-1)/size)
	for len(data) > size {
		chunks = append(chunks, data[:size])
		data = data[size:]
	}
	if len(data) > 0 {
		chunks = append(chunks, data)
	}
	return chunks
}

// ReadResponse 服务器端
// 用于从 WebSocket 连接中读取 HTTP 响应。它首先从 WebSocket 连接中读取一条消息，然后将该消息解析为 HTTP 响应，并返回该响应以及任何错误信息。
func ReadResponse(conn *websocket.Conn) (*http.Response, error) {
	_, message, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(message)), nil)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// CopyResponse
// 用于将 HTTP 响应复制到 WebSocket 连接。它首先将 HTTP 响应写入一个缓冲区，然后将该缓冲区写入 WebSocket 连接。
func CopyResponse(conn *websocket.Conn, resp *http.Response) error {
	var buf bytes.Buffer
	err := resp.Write(&buf)
	if err != nil {
		return err
	}

	return conn.WriteMessage(websocket.TextMessage, buf.Bytes())
}

var _requestId = rand.Uint32()

func GetNextRequestId() uint32 {
	_requestId += 1
	return _requestId
}
