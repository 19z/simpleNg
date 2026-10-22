package client

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type sshHandler struct{ directory string }

func NewSSHHandler(directory string) http.Handler { return &sshHandler{directory: directory} }

func (h *sshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/index.html" || strings.HasPrefix(r.URL.Path, "/__simple_ng_internal__/tunnel/") {
		serveTunnelUI(w, r)
		return
	}
	if r.URL.Path != "/ws/ssh" || !websocket.IsWebSocketUpgrade(r) {
		http.NotFound(w, r)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		origin, err := url.Parse(r.Header.Get("Origin"))
		return err == nil && strings.EqualFold(origin.Host, r.Host)
	}}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	var login struct {
		Columns int `json:"cols"`
		Rows    int `json:"rows"`
	}
	if ws.ReadJSON(&login) != nil {
		return
	}
	input := &terminalLogin{ws: ws}
	username, err := input.readLine("login: ", true)
	if err != nil {
		return
	}
	password, err := input.readLine("Password: ", false)
	if err != nil {
		return
	}
	config := &ssh.ClientConfig{User: username, Auth: []ssh.AuthMethod{ssh.Password(password)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 8 * time.Second}
	conn, err := ssh.Dial("tcp", "127.0.0.1:22", config)
	if err != nil {
		ws.WriteJSON(map[string]string{"type": "error", "message": "SSH login failed: " + err.Error()})
		return
	}
	defer conn.Close()
	session, err := conn.NewSession()
	if err != nil {
		ws.WriteJSON(map[string]string{"type": "error", "message": "Could not start SSH session"})
		return
	}
	defer session.Close()
	if login.Columns < 1 {
		login.Columns = 100
	}
	if login.Rows < 1 {
		login.Rows = 30
	}
	if err := session.RequestPty("xterm-256color", login.Rows, login.Columns, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); err != nil {
		ws.WriteJSON(map[string]string{"type": "error", "message": "PTY unavailable"})
		return
	}
	in, err := session.StdinPipe()
	if err != nil {
		return
	}
	out, err := session.StdoutPipe()
	if err != nil {
		return
	}
	errout, err := session.StderrPipe()
	if err != nil {
		return
	}
	if err := session.Start("cd " + shellQuote(h.directory) + " && exec ${SHELL:-/bin/sh} -i"); err != nil {
		ws.WriteJSON(map[string]string{"type": "error", "message": "Could not start shell"})
		return
	}
	if len(input.pending) > 0 {
		_, _ = in.Write(input.pending)
	}
	var writeMu sync.Mutex
	write := func(kind int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return ws.WriteMessage(kind, data)
	}
	for _, stream := range []interface{ Read([]byte) (int, error) }{out, errout} {
		go func(src interface{ Read([]byte) (int, error) }) {
			buf := make([]byte, 4096)
			for {
				n, e := src.Read(buf)
				if n > 0 && write(websocket.BinaryMessage, buf[:n]) != nil {
					return
				}
				if e != nil {
					return
				}
			}
		}(stream)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			kind, data, e := ws.ReadMessage()
			if e != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				if _, e = in.Write(data); e != nil {
					return
				}
				continue
			}
			var resize struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &resize) == nil && resize.Type == "resize" && resize.Cols > 0 && resize.Rows > 0 {
				_ = session.WindowChange(resize.Rows, resize.Cols)
			}
		}
	}()
	waitDone := make(chan struct{})
	go func() { _ = session.Wait(); close(waitDone) }()
	select {
	case <-done:
	case <-waitDone:
		_ = write(websocket.TextMessage, []byte(`{"type":"error","message":"Remote shell exited"}`))
	}
}

// Login input stays in the terminal; passwords are never echoed to the browser.
type terminalLogin struct {
	ws      *websocket.Conn
	pending []byte
}

func (t *terminalLogin) readLine(prompt string, echo bool) (string, error) {
	if err := t.ws.WriteMessage(websocket.BinaryMessage, []byte(prompt)); err != nil {
		return "", err
	}
	var line []byte
	for {
		if len(t.pending) == 0 {
			kind, data, err := t.ws.ReadMessage()
			if err != nil {
				return "", err
			}
			if kind != websocket.BinaryMessage {
				continue
			}
			t.pending = data
		}
		b := t.pending[0]
		t.pending = t.pending[1:]
		switch b {
		case '\r', '\n':
			if b == '\r' && len(t.pending) > 0 && t.pending[0] == '\n' {
				t.pending = t.pending[1:]
			}
			if err := t.ws.WriteMessage(websocket.BinaryMessage, []byte("\r\n")); err != nil {
				return "", err
			}
			return string(line), nil
		case 3:
			line = nil
			_ = t.ws.WriteMessage(websocket.BinaryMessage, []byte("^C\r\n"+prompt))
		case 127, 8:
			if len(line) > 0 {
				line = line[:len(line)-1]
				if echo {
					_ = t.ws.WriteMessage(websocket.BinaryMessage, []byte("\b \b"))
				}
			}
		default:
			if b >= 32 && len(line) < 4096 {
				line = append(line, b)
				if echo {
					_ = t.ws.WriteMessage(websocket.BinaryMessage, []byte{b})
				}
			}
		}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
