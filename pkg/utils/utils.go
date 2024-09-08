package utils

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
)

// CopyRequest 服务器端
// 这个函数的作用是将一个HTTP请求写入到一个WebSocket连接中。它假设请求没有被修改过，并且返回一个错误如果写入或读取请求失败。
func CopyRequest(requestId uint32, conn *websocket.Conn, req *http.Request) error {
	var requestBuf bytes.Buffer
	err := req.Write(&requestBuf)
	if err != nil {
		return err
	}
	prefix := []byte{0xff, 0x00, 0x00, 0x00}
	prefix = append(prefix, make([]byte, 4)...)
	binary.LittleEndian.PutUint32(prefix[4:], requestId)
	err = conn.WriteMessage(websocket.BinaryMessage, GzipEncode(append(prefix, requestBuf.Bytes()...)))
	if err != nil {
		return err
	}
	return nil
}

var ErrInvalidRequestPrefix = fmt.Errorf("invalid request prefix")

func ResumeRequest2(data []byte, local string) (uint32, *http.Request, error) {
	// 验证前缀是否正确
	if len(data) < 8 || data[0] != 0xff || data[1] != 0x00 || data[2] != 0x00 || data[3] != 0x00 {
		return 0, nil, ErrInvalidRequestPrefix
	}

	// 读取requestId
	requestId := binary.LittleEndian.Uint32(data[4:8])

	// 去掉前缀，获取原始请求数据
	requestBuf := data[8:]
	// 解析请求字符串
	parts := bytes.SplitN(requestBuf, []byte("\r\n\r\n"), 2)
	headerLines := bytes.Split(parts[0], []byte("\r\n"))
	body := parts[1]

	// 解析请求行
	requestLine := strings.Split(strings.TrimSpace(string(headerLines[0])), " ")
	method := requestLine[0]
	path := requestLine[1]

	// 解析请求头
	headers := make(http.Header)
	for _, line := range headerLines[1:] {
		headerParts := strings.SplitN(string(line), ": ", 2)
		headers.Add(headerParts[0], headerParts[1])
	}

	// 创建 http.Request 对象
	req, err := http.NewRequest(method, path, bytes.NewReader(body))
	if err != nil {
		fmt.Println("Error creating request:", err)
		return 0, nil, err
	}

	// 设置请求头
	req.Header = headers

	// 设置请求的 Content-Length
	req.ContentLength = int64(len(body))

	// 替换host为本地的port端口
	req.Host = local
	req.URL.Host = req.Host
	req.URL.Scheme = "http"
	req.RequestURI = "" // 将请求URI设置为空字符串，否则会出现错误

	return requestId, req, nil
}

func ResumeRequest3(data []byte) (uint32, []byte, error) {
	// 验证前缀是否正确
	if len(data) < 8 || data[0] != 0xff || data[1] != 0x00 || data[2] != 0x00 || data[3] != 0x00 {
		return 0, nil, ErrInvalidRequestPrefix
	}

	// 读取requestId
	requestId := binary.LittleEndian.Uint32(data[4:8])

	// 去掉前缀，获取原始请求数据
	requestBuf := data[8:]

	return requestId, requestBuf, nil
}

// ClientRequest 客户端
// 客户端，客户端使用此方法发起使用 ResumeRequest 解析出的请求。同时，流式读取输出内容以每 2048 字节为一块发送到 WebSocket 连接中。
// 每个块使用 0xff000002 作为前缀，然后紧接着4个字节的 requestId。
// 如果正文结束，最后一个快以 0xff000003 + requestId作为前缀。
// 如果连接失败，则返回 0xff000004 + requestId
func ClientRequest(requestId uint32, conn *websocket.Conn, req *http.Request) error {
	// 发送 HTTP 请求并获取响应
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	log.Printf("request: %v", req)
	resp, err := client.Do(req)
	if err != nil {
		// 连接失败，返回 0xff000004 + requestId
		return WriteToConnect(0xff000004, requestId, []byte(err.Error()), conn)
	}
	defer resp.Body.Close()
	return ClientResponse(requestId, conn, resp)
}

func ClientResponse(requestId uint32, conn *websocket.Conn, resp *http.Response) error {
	// 读取响应头
	respBytes, err := httputil.DumpResponse(resp, false)
	if err != nil {
		return WriteToConnect(0xff000004, requestId, []byte(err.Error()), conn)
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
			return WriteToConnect(0xff000004, requestId, nil, conn)
		}

		if err == io.EOF {
			// 正文结束，发送 0xff000003 + requestId
			//endPrefix := make([]byte, 8)
			//binary.BigEndian.PutUint32(endPrefix, 0xff000003)
			//binary.BigEndian.PutUint32(endPrefix[4:], requestId)
			//log.Printf("write: %v %s", endPrefix, string(buffer[:n]))
			//conn.WriteMessage(websocket.BinaryMessage, endPrefix)
			if respBytes != nil {
				err = WriteToConnect(0xff000003, requestId, append(respBytes, buffer[:n]...), conn)
				respBytes = nil
				return err
			}
			err = WriteToConnect(0xff000003, requestId, buffer[:n], conn)
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
				err = WriteToConnect(0xff000002, requestId, append(respBytes, buffer[:n]...), conn)
				respBytes = nil
				if err != nil {
					return err
				}
			} else {
				err = WriteToConnect(0xff000002, requestId, buffer[:n], conn)
				if err != nil {
					return err
				}
			}

		}
	}

	return nil
}

func WriteToConnect(prefix uint32, requestId uint32, body []byte, conn *websocket.Conn) error {
	if body == nil {
		body = []byte{}
	}
	data := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(data, prefix)
	binary.BigEndian.PutUint32(data[4:], requestId)
	copy(data[8:], body)
	//zipData := GzipEncode(data)
	//log.Printf("write: %v %d %s", data[0:8], len(zipData), string(body[0:min(len(body), 100)]))
	err := conn.WriteMessage(websocket.BinaryMessage, GzipEncode(data))
	if err != nil {
		return err
	}
	return nil
}

var _requestId = uint32(0)

func GetNextRequestId() uint32 {
	_requestId += 1
	return _requestId
}

// ParseMessage 解析消息
// 解析消息中的前缀，requestId，以及正文
// prefix 为 0xff000002 数据块，0xff000003 为正文结束，0xff000004 为连接失败
func ParseMessage(message []byte) (prefix uint32, requestId uint32, body []byte, err error) {
	// 解析前缀
	prefix = binary.BigEndian.Uint32(message)
	if prefix != 0xff000002 && prefix != 0xff000003 && prefix != 0xff000004 {
		err = fmt.Errorf("Invalid message prefix: %d", prefix)
		return
	}
	// 解析requestId
	requestId = binary.BigEndian.Uint32(message[4:])
	// 解析正文
	body = message[8:]
	return
}

// GzipEncode 函数将输入的字节切片进行 Gzip 压缩并返回压缩后的字节切片
func GzipEncode(data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)

	_, err := gz.Write(data)
	if err != nil {
		log.Println(err)
		return nil
	}

	if err := gz.Close(); err != nil {
		return nil
	}

	return buf.Bytes()
}

// GzipDecode 函数将输入的 Gzip 压缩字节切片进行解压缩，并返回解压后的字节切片
func GzipDecode(data []byte) []byte {
	buf := bytes.NewBuffer(data)
	gz, err := gzip.NewReader(buf)
	if err != nil {
		log.Println(err)
		return nil
	}
	defer gz.Close()

	uncompressedData, err := io.ReadAll(gz)
	if err != nil {
		log.Println(err)
		return nil
	}

	return uncompressedData
}
