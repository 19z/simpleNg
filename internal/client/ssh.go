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
		Username string `json:"username"`
		Password string `json:"password"`
		Columns  int    `json:"cols"`
		Rows     int    `json:"rows"`
	}
	if ws.ReadJSON(&login) != nil || login.Username == "" {
		return
	}
	config := &ssh.ClientConfig{User: login.Username, Auth: []ssh.AuthMethod{ssh.Password(login.Password)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 8 * time.Second}
	conn, err := ssh.Dial("tcp", "127.0.0.1:22", config)
	if err != nil {
		ws.WriteJSON(map[string]string{"type": "error", "message": "SSH login failed"})
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
	if err := session.Start("cd " + shellQuote(h.directory) + " && exec ${SHELL:-/bin/sh} -l"); err != nil {
		ws.WriteJSON(map[string]string{"type": "error", "message": "Could not start shell"})
		return
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
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
