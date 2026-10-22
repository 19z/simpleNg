package client

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCollectorUploadsAndListsUnreadableFiles(t *testing.T) {
	root := t.TempDir()
	handler, err := NewCollectorHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "../secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("payload"))
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%q", res.Code, res.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "secret.txt"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("uploaded data=%q err=%v", data, err)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "secret.txt") {
		t.Fatalf("list status=%d body=%q", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/files/secret.txt", nil))
	if res.Code != http.StatusOK || res.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("upload attachment status=%d disposition=%q", res.Code, res.Header().Get("Content-Disposition"))
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/secret.txt", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("uploaded file was directly viewable: %d", res.Code)
	}
}

func TestLocalURLParsing(t *testing.T) {
	root := t.TempDir()
	collectURL := "collect://" + filepath.ToSlash(root)
	if runtime.GOOS == "windows" {
		collectURL = "collect:///" + filepath.ToSlash(root)
	}
	if _, err := NewLocalHandler(collectURL); err != nil {
		t.Fatal(err)
	}
	relativeRoot := filepath.Join("collect-relative-test")
	if err := os.Mkdir(relativeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(relativeRoot)
	for _, value := range []string{"file://./collect-relative-test", "file://./", "collect://./", "collect://./collect-relative-test"} {
		if _, err := NewLocalHandler(value); err != nil {
			t.Fatalf("relative URL %q: %v", value, err)
		}
	}
	bareSSH, err := url.Parse("ssh://")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := localURLPath(bareSSH); err != nil || got != "" {
		t.Fatalf("bare ssh URL path = %q, %v; want login user's default directory", got, err)
	}
	relativeSSH, err := url.Parse("ssh://./")
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := localURLPath(relativeSSH); err != nil || got != cwd {
		t.Fatalf("relative ssh URL path = %q, %v; want current directory %q", got, err, cwd)
	}
	if runtime.GOOS == "windows" {
		if _, err := NewLocalHandler("ssh:///C:/tmp"); err == nil {
			t.Fatal("SSH should be unsupported on Windows")
		}
	}
	for _, value := range []string{"collect:relative", "collect://host/path", "collect:///not/a/real/directory"} {
		if _, err := NewLocalHandler(value); err == nil {
			t.Errorf("accepted invalid local URL %q", value)
		}
	}
}

func TestEmbeddedUIIsServed(t *testing.T) {
	handler := NewSSHHandler("/")
	for _, route := range []string{"/", "/__simple_ng_internal__/tunnel/"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, route, nil))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "simpleNg") {
			t.Fatalf("UI route %s status=%d", route, res.Code)
		}
	}
}
