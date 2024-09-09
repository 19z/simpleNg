package utils

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"log"
)

var ErrInvalidRequestPrefix = fmt.Errorf("invalid request prefix")

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
