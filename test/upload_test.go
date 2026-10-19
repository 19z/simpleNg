package test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestFileUpload(t *testing.T) {
	content := bytes.Repeat([]byte("这是一个测试文件内容"), 50000)
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		io.Copy(w, file)
	}))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	file.Write(content)
	writer.Close()
	resp, err := proxy.http.Post(proxy.url+"/upload", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(got, content) {
		t.Fatalf("upload: status=%d, bytes=%d, err=%v, body=%s", resp.StatusCode, len(got), err, strings.TrimSpace(string(got[:min(len(got), 100)])))
	}
}
