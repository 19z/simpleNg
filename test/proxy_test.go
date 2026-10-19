package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"simpleNg/internal/client"
	"simpleNg/internal/server"
	"simpleNg/internal/tunnel"
	"simpleNg/pkg/config"

	"github.com/gorilla/websocket"
)

type proxyFixture struct {
	url    string
	http   *http.Client
	srv    *server.Server
	client *client.Client
}

func startClient(t *testing.T, url, local string) *client.Client {
	t.Helper()
	cli, err := client.NewClient(&config.ClientConfig{Domain: "ws" + strings.TrimPrefix(url, "http"), Local: local})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		cli.MessageHandler()
		close(done)
	}()
	t.Cleanup(func() {
		cli.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("client handler did not stop")
		}
	})
	return cli
}

func newProxy(t *testing.T, handler http.Handler) *proxyFixture {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	srv, err := server.NewServer(&config.ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	t.Cleanup(public.Close)
	t.Cleanup(func() { srv.Close() })
	cli := startClient(t, public.URL, strings.TrimPrefix(upstream.URL, "http://"))
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	return &proxyFixture{url: public.URL, http: &http.Client{Transport: transport, Timeout: 5 * time.Second}, srv: srv, client: cli}
}

func TestHTTPBodiesAndTrailers(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Method", r.Method)
		w.Header().Set("X-URI", r.RequestURI)
		w.Header().Set("X-Host", r.Host)
		w.Header().Set("Trailer", "X-Complete")
		w.WriteHeader(http.StatusCreated)
		w.Write(body)
		w.Header().Set("X-Complete", "yes")
	}))
	content := bytes.Repeat([]byte("\x00\x01payload"), 100000)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "GET"} {
		for _, chunked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/chunked=%t", method, chunked), func(t *testing.T) {
				req, _ := http.NewRequest(method, proxy.url+"/echo%2Fpart?q=a%2Bb", bytes.NewReader(content))
				if chunked {
					req.ContentLength = -1
				}
				resp, err := proxy.http.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				got, err := io.ReadAll(resp.Body)
				if err != nil || resp.StatusCode != http.StatusCreated || !bytes.Equal(got, content) {
					t.Fatalf("body: status=%d, length=%d, err=%v", resp.StatusCode, len(got), err)
				}
				if resp.Header.Get("X-Method") != method || resp.Header.Get("X-URI") != "/echo%2Fpart?q=a%2Bb" || resp.Header.Get("X-Host") != req.URL.Host || resp.Trailer.Get("X-Complete") != "yes" {
					t.Fatalf("headers/trailers were not preserved: %v, %v", resp.Header, resp.Trailer)
				}
			})
		}
	}
}

func echoWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/reject" {
		w.Header().Set("X-Rejected", "yes")
		http.Error(w, "denied", http.StatusForbidden)
		return
	}
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"events.v1"}, EnableCompression: true,
		CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "https://app.example.com" },
	}
	conn, err := upgrader.Upgrade(w, r, http.Header{"X-Upstream": []string{r.RequestURI + "|" + r.Header.Get("Cookie")}})
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetCloseHandler(func(code int, text string) error {
		return conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
	})
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); ok {
				conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeErr.Code, closeErr.Text), time.Now().Add(time.Second))
			}
			return
		}
		if err := conn.WriteMessage(kind, data); err != nil {
			return
		}
	}
}

func dialWebSocket(url string) (*websocket.Conn, *http.Response, error) {
	dialer := websocket.Dialer{Subprotocols: []string{"events.v1"}, EnableCompression: true, HandshakeTimeout: 3 * time.Second}
	return dialer.Dial("ws"+strings.TrimPrefix(url, "http"), http.Header{
		"Origin": []string{"https://app.example.com"}, "Cookie": []string{"token=abc"},
	})
}

func TestWebSocketFramesAndHandshake(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(echoWebSocket))
	conn, resp, err := dialWebSocket(proxy.url + "/events?token=a%2Bb")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if conn.Subprotocol() != "events.v1" || !strings.Contains(resp.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate") || resp.Header.Get("X-Upstream") != "/events?token=a%2Bb|token=abc" {
		t.Fatalf("handshake headers were not preserved: %v", resp.Header)
	}
	for _, frame := range []struct {
		kind int
		data []byte
	}{
		{websocket.TextMessage, []byte(`{"type":"open","endpoint":"$events"}`)},
		{websocket.BinaryMessage, append([]byte{0, 0, 0, 1}, bytes.Repeat([]byte("binary data"), 100000)...)},
		{websocket.BinaryMessage, nil},
		{websocket.TextMessage, []byte("after binary")},
	} {
		if err := conn.WriteMessage(frame.kind, frame.data); err != nil {
			t.Fatal(err)
		}
		kind, got, err := conn.ReadMessage()
		if err != nil || kind != frame.kind || !bytes.Equal(got, frame.data) {
			t.Fatalf("echo: kind=%d, length=%d, err=%v", kind, len(got), err)
		}
	}
	pong := false
	conn.SetPongHandler(func(data string) error { pong = data == "ping"; return nil })
	conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(time.Second))
	conn.WriteMessage(websocket.TextMessage, []byte("after ping"))
	if _, _, err := conn.ReadMessage(); err != nil || !pong {
		t.Fatalf("ping/pong: pong=%t, err=%v", pong, err)
	}
	conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
	_, _, err = conn.ReadMessage()
	if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseNormalClosure || closeErr.Text != "done" {
		t.Fatalf("close frame: %v", err)
	}
}

func TestRejectedHandshakeDoesNotDisconnectTunnel(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(echoWebSocket))
	_, resp, err := dialWebSocket(proxy.url + "/reject")
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Rejected") != "yes" {
		t.Fatalf("rejected handshake: response=%v, err=%v", resp, err)
	}
	resp.Body.Close()
	conn, _, err := dialWebSocket(proxy.url + "/events")
	if err != nil {
		t.Fatalf("tunnel was disconnected: %v", err)
	}
	conn.Close()
}

func TestConcurrentWebSocketsAndHTTP(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			echoWebSocket(w, r)
			return
		}
		io.WriteString(w, "ok")
	}))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _, err := dialWebSocket(proxy.url + "/events")
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for n := 0; n < 5; n++ {
				if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0, 0, 0, 1, byte(n)}); err != nil {
					t.Error(err)
					return
				}
				if kind, got, err := conn.ReadMessage(); err != nil || kind != websocket.BinaryMessage || !bytes.Equal(got, []byte{0, 0, 0, 1, byte(n)}) {
					t.Errorf("concurrent echo: kind=%d, data=%v, err=%v", kind, got, err)
					return
				}
				resp, err := proxy.http.Get(proxy.url)
				if err != nil {
					t.Error(err)
					return
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || string(body) != "ok" {
					t.Errorf("concurrent HTTP: %s, %v", body, err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestMissingClientReturnsImmediately(t *testing.T) {
	srv, _ := server.NewServer(&config.ServerConfig{})
	public := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	defer public.Close()
	defer srv.Close()
	for _, upgrade := range []bool{false, true} {
		req, _ := http.NewRequest("GET", public.URL+"/events", nil)
		if upgrade {
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := http.DefaultClient.Do(req.WithContext(ctx))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("missing client: status=%d", resp.StatusCode)
		}
	}
}

func TestSlowRequestCancellationAndDisconnect(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		io.WriteString(w, "ok")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", proxy.url+"/slow", nil)
	done := make(chan struct{})
	go func() {
		resp, _ := proxy.http.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("slow request did not reach upstream")
	}
	resp, err := proxy.http.Get(proxy.url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("slow stream blocked other traffic: %d", resp.StatusCode)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Error("cancellation did not close upstream connection")
		proxy.client.Close()
	}
	<-done
	proxy.client.Close()
	resp, err = proxy.http.Get(proxy.url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("closed tunnel: status=%d", resp.StatusCode)
	}
}

func TestClientReplacementKeepsNewTunnel(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(echoWebSocket))
	conn, _, err := dialWebSocket(proxy.url + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "replacement") }))
	defer upstream.Close()
	startClient(t, proxy.url, strings.TrimPrefix(upstream.URL, "http://"))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Error("old WebSocket stayed connected after tunnel replacement")
	}
	for i := 0; i < 10; i++ {
		resp, err := proxy.http.Get(proxy.url)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(got) != "replacement" {
			t.Fatalf("old cleanup removed new tunnel: %q, %v", got, err)
		}
	}
}

func TestUnavailableLocalService(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	startClient(t, proxy.url, address)
	resp, err := proxy.http.Get(proxy.url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusBadGateway || string(got) != "Local service unavailable" {
		t.Fatalf("unavailable local service: status=%d, body=%q, err=%v", resp.StatusCode, got, err)
	}
}

func TestOnlyReservedPathRegistersTunnel(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(echoWebSocket))
	for _, path := range []string{"/", "/events", tunnel.Path + "/business"} {
		for _, protocols := range [][]string{nil, {tunnel.Subprotocol}} {
			dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 3 * time.Second}
			conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(proxy.url, "http")+path, http.Header{"Origin": []string{"https://app.example.com"}})
			if err != nil {
				t.Fatalf("business path %s protocols=%v: %v", path, protocols, err)
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			conn.WriteMessage(websocket.TextMessage, []byte("business"))
			_, got, err := conn.ReadMessage()
			conn.Close()
			if err != nil || string(got) != "business" {
				t.Fatalf("business path was treated as tunnel: %q, %v", got, err)
			}
		}
	}
	resp, err := proxy.http.Get(proxy.url + tunnel.Path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("reserved path accepted HTTP: %d", resp.StatusCode)
	}
	_, resp, err = dialWebSocket(proxy.url + tunnel.Path)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("reserved path accepted a business handshake: %v, %v", resp, err)
	}
	resp.Body.Close()
	conn, _, err := dialWebSocket(proxy.url + "/events")
	if err != nil {
		t.Fatalf("reserved path rejection disconnected tunnel: %v", err)
	}
	conn.Close()
}

func TestStreamingResponse(t *testing.T) {
	cancelled := make(chan struct{})
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: hello\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", proxy.url+"/stream", nil)
	resp, err := proxy.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := make([]byte, len("data: hello\n\n"))
	if _, err := io.ReadFull(resp.Body, got); err != nil || string(got) != "data: hello\n\n" {
		t.Fatalf("stream was not flushed: %q, %v", got, err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Error("stream cancellation was not forwarded")
		proxy.client.Close()
	}
}

func TestDomainsRemainIndependent(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "original") }))
	_, port, err := net.SplitHostPort(strings.TrimPrefix(proxy.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	otherURL := "http://localhost:" + port
	otherUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "other domain") }))
	defer otherUpstream.Close()
	otherClient := startClient(t, otherURL, strings.TrimPrefix(otherUpstream.URL, "http://"))
	for address, expected := range map[string]string{proxy.url: "original", otherURL: "other domain"} {
		resp, err := proxy.http.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(got) != expected {
			t.Fatalf("wrong domain routed: %q, %v", got, err)
		}
	}
	otherClient.Close()
	resp, err := proxy.http.Get(proxy.url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || string(got) != "original" {
		t.Fatalf("disconnect affected other domain: %q, %v", got, err)
	}
}

func waitForBody(t *testing.T, proxy *proxyFixture, expected string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := proxy.http.Get(proxy.url)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && string(body) == expected {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for proxied body %q", expected)
}

func TestNetworkDisconnectReconnectsToReplacementServer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "reconnected") }))
	defer upstream.Close()
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
	proxy := &proxyFixture{
		url:  public.URL,
		http: &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second},
	}
	defer proxy.http.Transport.(*http.Transport).CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- client.RunWithReconnect(ctx, &config.ClientConfig{
			Domain: "ws" + strings.TrimPrefix(public.URL, "http"),
			Local:  strings.TrimPrefix(upstream.URL, "http://"),
		})
	}()
	waitForBody(t, proxy, "reconnected")
	current.Store(second)
	first.Close()
	waitForBody(t, proxy, "reconnected")
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnecting client did not stop after cancellation")
	}
}

func TestTakenOverClientStopsReconnecting(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "old client") }))
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "retrying client") }))
	defer oldUpstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- client.RunWithReconnect(ctx, &config.ClientConfig{
			Domain: "ws" + strings.TrimPrefix(proxy.url, "http"),
			Local:  strings.TrimPrefix(oldUpstream.URL, "http://"),
		})
	}()
	waitForBody(t, proxy, "retrying client")
	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "new client") }))
	defer newUpstream.Close()
	startClient(t, proxy.url, strings.TrimPrefix(newUpstream.URL, "http://"))
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("taken over client returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("taken over client did not stop")
	}
	waitForBody(t, proxy, "new client")
}
