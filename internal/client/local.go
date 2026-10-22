package client

import (
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func isLocalPage(value string) bool {
	value = strings.ToLower(value)
	return strings.HasPrefix(value, "file:") || strings.HasPrefix(value, "collect:") || strings.HasPrefix(value, "ssh:")
}

func NewLocalHandler(value string) (http.Handler, error) {
	u, err := url.Parse(value)
	if err != nil || u.Opaque != "" || (u.Host != "" && u.Host != ".") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("local URL must contain an absolute path: %q", value)
	}
	root := filepath.FromSlash(u.Path)
	if u.Host == "." {
		root = filepath.FromSlash(strings.TrimPrefix(u.Path, "/"))
	}
	if root == "" {
		root = "."
	}
	if runtime.GOOS == "windows" && len(root) > 2 && root[0] == '\\' && root[2] == ':' {
		root = root[1:]
	}
	if !filepath.IsAbs(root) {
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("could not resolve local URL path: %w", err)
		}
	}
	switch strings.ToLower(u.Scheme) {
	case "file":
		return NewDirectoryHandler(value)
	case "collect":
		return NewCollectorHandler(root)
	case "ssh":
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("webssh is not supported on Windows")
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("webssh default path must be an existing directory: %s", root)
		}
		if err := checkSSHService(); err != nil {
			return nil, err
		}
		return NewSSHHandler(root), nil
	default:
		return nil, fmt.Errorf("unsupported local URL scheme %q", u.Scheme)
	}
}

func checkSSHService() error {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:22", time.Second)
	if err != nil {
		return fmt.Errorf("webssh requires an sshd service listening on 127.0.0.1:22: %w", err)
	}
	conn.Close()
	return nil
}

func serveTunnelUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/index.html" || strings.HasPrefix(r.URL.Path, "/__simple_ng_internal__/tunnel/") {
		name := "index.html"
		if strings.HasPrefix(r.URL.Path, "/__simple_ng_internal__/tunnel/") {
			name = strings.TrimPrefix(r.URL.Path, "/__simple_ng_internal__/tunnel/")
		}
		if name == "" {
			name = "index.html"
		}
		f, err := tunnelAssets.Open("tunnelui/dist/" + filepath.ToSlash(name))
		if err == nil {
			defer f.Close()
			data, readErr := io.ReadAll(f)
			if readErr == nil {
				if contentType := mime.TypeByExtension(filepath.Ext(name)); contentType != "" {
					w.Header().Set("Content-Type", contentType)
				} else {
					w.Header().Set("Content-Type", http.DetectContentType(data))
				}
				w.Write(data)
				return
			}
		}
	}
	http.NotFound(w, r)
}
