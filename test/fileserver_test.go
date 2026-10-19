package test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"simpleNg/internal/client"
	"simpleNg/internal/server"
	"simpleNg/pkg/config"
)

func directoryURL(root string) string {
	root = filepath.ToSlash(root)
	if !strings.HasPrefix(root, "/") {
		root = "/" + root
	}
	return (&url.URL{Scheme: "file", Path: root}).String()
}

func TestDirectorySiteThroughTunnel(t *testing.T) {
	root := filepath.Join(t.TempDir(), "site space %20 中文")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"index.html": "preferred homepage", "index.htm": "fallback homepage",
		"nested/index.htm": "nested homepage", "asset.txt": "0123456789", "中文 %.txt": "encoded asset",
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	srv, _ := server.NewServer(&config.ServerConfig{})
	public := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	t.Cleanup(public.Close)
	t.Cleanup(func() { srv.Close() })
	startClient(t, public.URL, directoryURL(root))
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, tc := range []struct{ path, body string }{
		{"/", "preferred homepage"}, {"/nested/", "nested homepage"}, {"/asset.txt", "0123456789"},
		{"/" + url.PathEscape("中文 %.txt"), "encoded asset"},
	} {
		resp, err := httpClient.Get(public.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || string(body) != tc.body {
			t.Fatalf("GET %s: status=%d, body=%q, err=%v", tc.path, resp.StatusCode, body, err)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, public.URL+"/asset.txt", nil)
	req.Header.Set("Range", "bytes=2-5")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("range response: status=%d, body=%q, err=%v", resp.StatusCode, body, err)
	}
	resp, err = httpClient.Head(public.URL + "/asset.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || len(body) != 0 || resp.ContentLength != 10 {
		t.Fatalf("HEAD response: length=%d, body=%q, err=%v", resp.ContentLength, body, err)
	}
	req, _ = http.NewRequest(http.MethodGet, public.URL+"/asset.txt", nil)
	req.Header.Set("If-Modified-Since", resp.Header.Get("Last-Modified"))
	resp, err = httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional response: status=%d", resp.StatusCode)
	}
	noRedirect := *httpClient
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err = noRedirect.Get(public.URL + "/nested?q=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/nested/?q=1" {
		t.Fatalf("directory redirect: status=%d, headers=%v", resp.StatusCode, resp.Header)
	}
	if err := os.Remove(filepath.Join(root, "index.html")); err != nil {
		t.Fatal(err)
	}
	resp, err = httpClient.Get(public.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "fallback homepage" {
		t.Fatalf("index.htm fallback: %q, %v", body, err)
	}
	resp, err = httpClient.Get(public.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing file: status=%d", resp.StatusCode)
	}
}

func TestInvalidDirectoryDoesNotConnectOrRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := client.RunWithReconnect(ctx, &config.ClientConfig{Domain: "ws://127.0.0.1:1", Local: directoryURL(filepath.Join(t.TempDir(), "missing"))})
	if err == nil || ctx.Err() != nil {
		t.Fatalf("invalid directory was not rejected immediately: %v, ctx=%v", err, ctx.Err())
	}
}

func TestDirectoryReconnectAndTakeover(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("directory homepage"), 0600); err != nil {
		t.Fatal(err)
	}
	first, _ := server.NewServer(&config.ServerConfig{})
	second, _ := server.NewServer(&config.ServerConfig{})
	var current atomic.Pointer[server.Server]
	current.Store(first)
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current.Load().HandleRequest(w, r)
	}))
	defer public.Close()
	defer first.Close()
	defer second.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	proxy := &proxyFixture{url: public.URL, http: &http.Client{Transport: transport, Timeout: 3 * time.Second}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- client.RunWithReconnect(ctx, &config.ClientConfig{
			Domain: "ws" + strings.TrimPrefix(public.URL, "http"), Local: directoryURL(root),
		})
	}()
	waitForBody(t, proxy, "directory homepage")
	current.Store(second)
	first.Close()
	waitForBody(t, proxy, "directory homepage")
	newRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(newRoot, "index.htm"), []byte("new homepage"), 0600); err != nil {
		t.Fatal(err)
	}
	startClient(t, public.URL, directoryURL(newRoot))
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("directory client did not stop after takeover")
	}
	waitForBody(t, proxy, "new homepage")
}
