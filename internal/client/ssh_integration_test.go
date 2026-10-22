package client

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Run inside the ssh-test container with SIMPLENG_SSH_TEST=1.
func TestSSHIntegration(t *testing.T) {
	if os.Getenv("SIMPLENG_SSH_TEST") != "1" {
		t.Skip("requires the local ssh-test container")
	}
	if err := checkSSHService(); err != nil {
		if _, localErr := NewLocalHandler("ssh://./"); localErr == nil || !strings.Contains(localErr.Error(), "sshd service") {
			t.Fatalf("ssh://./ should report a missing sshd, got %v", localErr)
		}
		return
	}
	for _, value := range []string{"ssh://./", "ssh://./home/ssh-test/work"} {
		if _, err := NewLocalHandler(value); err != nil {
			t.Fatalf("container SSH mode should be available with sshd: %v", err)
		}
	}
	if err := checkSSHService(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewSSHHandler(filepath.Join("/home/ssh-test", "work")))
	defer server.Close()
	for _, password := range []string{"incorrect", "changeme"} {
		ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/ssh", http.Header{"Origin": []string{server.URL}})
		if err != nil {
			t.Fatal(err)
		}
		ws.SetReadDeadline(time.Now().Add(15 * time.Second))
		if err := ws.WriteJSON(map[string]any{"username": "ssh-test", "password": password, "cols": 80, "rows": 24}); err != nil {
			t.Fatal(err)
		}
		if password == "incorrect" {
			_, data, err := ws.ReadMessage()
			if err != nil || !strings.Contains(string(data), "SSH login failed") {
				t.Fatalf("invalid login: %s %v", data, err)
			}
			ws.Close()
			continue
		}
		var output strings.Builder
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte("stty -echo\n")); err != nil {
			t.Fatal(err)
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, []byte("pwd; whoami; printf 'SSH_%s_OK\\n' TEST\n")); err != nil {
			t.Fatal(err)
		}
		for !strings.Contains(output.String(), "SSH_TEST_OK") {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("shell output: %s: %v", output.String(), err)
			}
			if kind != websocket.BinaryMessage {
				t.Fatalf("unexpected response: %s", data)
			}
			output.Write(data)
		}
		if !strings.Contains(output.String(), "/home/ssh-test/work") || !strings.Contains(output.String(), "ssh-test") {
			t.Fatalf("unexpected shell: %s", output.String())
		}
		ws.Close()
		t.Log("password authentication, PTY, working directory and shell command passed")
	}
}
