package test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"simpleNg/internal/client"
	"simpleNg/internal/server"
	"simpleNg/internal/tunnel"
	"simpleNg/pkg/config"

	"github.com/gorilla/websocket"
)

func TestTakeoverSendsWebSocketCloseReason(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	dialer := websocket.Dialer{Subprotocols: []string{tunnel.Subprotocol}, HandshakeTimeout: time.Second}
	old, _, err := dialer.Dial("ws"+strings.TrimPrefix(proxy.url, "http")+tunnel.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	old.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, ready, err := old.ReadMessage()
	if err != nil || string(ready) != tunnel.Ready {
		t.Fatalf("registration failed: %q, %v", ready, err)
	}
	startClient(t, proxy.url, "127.0.0.1:1")
	_, _, err = old.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != tunnel.CloseCodeTakenOver || closeErr.Text != tunnel.CloseReasonTakenOver {
		t.Fatalf("expected explicit takeover close frame, got %v", err)
	}
}

func TestClientReportsTakeover(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	old, err := client.NewClient(&config.ClientConfig{Domain: "ws" + strings.TrimPrefix(proxy.url, "http"), Local: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	finished := make(chan error, 1)
	go func() { finished <- old.MessageHandler() }()
	startClient(t, proxy.url, "127.0.0.1:1")
	select {
	case err := <-finished:
		if !errors.Is(err, client.ErrTakenOver) {
			t.Fatalf("takeover was reported as a network error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("taken over handler did not finish")
	}
}

func TestReconnectBackoffAndInitialFailureRecovery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "recovered") }))
	defer upstream.Close()
	srv, _ := server.NewServer(&config.ServerConfig{})
	attempts := make(chan time.Time, 8)
	var count atomic.Int32
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tunnel.Path {
			attempts <- time.Now()
			if count.Add(1) <= 2 {
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		srv.HandleRequest(w, r)
	}))
	defer public.Close()
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- client.RunWithReconnect(ctx, &config.ClientConfig{
			Domain: "ws" + strings.TrimPrefix(public.URL, "http"),
			Local:  strings.TrimPrefix(upstream.URL, "http://"),
		})
	}()
	var previous time.Time
	for i := 0; i < 3; i++ {
		select {
		case attempt := <-attempts:
			if i > 0 {
				expected := time.Second * time.Duration(1<<(i-1))
				if elapsed := attempt.Sub(previous); elapsed < expected-100*time.Millisecond {
					t.Fatalf("attempt %d retried too soon: %s, expected at least %s", i+1, elapsed, expected)
				}
			}
			previous = attempt
		case <-time.After(5 * time.Second):
			t.Fatal("client did not retry")
		}
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	waitForBody(t, &proxyFixture{url: public.URL, http: &http.Client{Transport: transport, Timeout: time.Second}}, "recovered")
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("connected client did not stop after cancellation")
	}
}

func TestReconnectCancellationDuringRegistration(t *testing.T) {
	upgraded := make(chan struct{})
	serverDone := make(chan struct{})
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{tunnel.Subprotocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(serverDone)
		close(upgraded)
		// Deliberately withhold Ready to exercise registration cancellation.
		conn.ReadMessage()
	}))
	defer public.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- client.RunWithReconnect(ctx, &config.ClientConfig{Domain: "ws" + strings.TrimPrefix(public.URL, "http")})
	}()
	select {
	case <-upgraded:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not reach registration")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("registration cancellation waited for handshake timeout")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("registration cancellation leaked the connection")
	}
}

func TestReconnectCancellationDuringBackoff(t *testing.T) {
	attempted := make(chan struct{}, 1)
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempted <- struct{}{}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer public.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- client.RunWithReconnect(ctx, &config.ClientConfig{Domain: "ws" + strings.TrimPrefix(public.URL, "http")})
	}()
	select {
	case <-attempted:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not attempt connection")
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancellation did not interrupt backoff")
	}
}

func TestCompetingReconnectClientsKeepNewestOwner(t *testing.T) {
	proxy := newProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "initial") }))
	competingUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "competing") }))
	defer competingUpstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const competitors = 8
	finished := make(chan error, competitors)
	start := make(chan struct{})
	for i := 0; i < competitors; i++ {
		go func() {
			<-start
			finished <- client.RunWithReconnect(ctx, &config.ClientConfig{
				Domain: "ws" + strings.TrimPrefix(proxy.url, "http"),
				Local:  strings.TrimPrefix(competingUpstream.URL, "http://"),
			})
		}()
	}
	close(start)
	trafficCtx, stopTraffic := context.WithCancel(ctx)
	trafficDone := make(chan struct{})
	go func() {
		defer close(trafficDone)
		for trafficCtx.Err() == nil {
			req, _ := http.NewRequestWithContext(trafficCtx, "GET", proxy.url, nil)
			resp, _ := proxy.http.Do(req)
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
	}()
	defer func() {
		stopTraffic()
		<-trafficDone
	}()
	for i := 0; i < competitors-1; i++ {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("competing clients kept reconnecting instead of accepting takeover")
		}
	}
	waitForBody(t, proxy, "competing")
	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "newest") }))
	defer newUpstream.Close()
	startClient(t, proxy.url, strings.TrimPrefix(newUpstream.URL, "http://"))
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("last competing client did not yield to the newest owner")
	}
	waitForBody(t, proxy, "newest")
}
