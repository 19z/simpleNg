package client

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type directoryHandler struct {
	root fs.FS
}

// NewDirectoryHandler serves a local directory named by an absolute file URL.
func NewDirectoryHandler(root string) (http.Handler, error) {
	parsed, err := url.Parse(root)
	if err != nil || !strings.EqualFold(parsed.Scheme, "file") || (parsed.Host != "" && parsed.Host != ".") || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, fmt.Errorf("local directory must be a file:/// URL: %q", root)
	}
	directory := parsed.Path
	if directory == "" || parsed.Opaque != "" || parsed.User != nil {
		return nil, fmt.Errorf("file URL must specify a directory")
	}
	directory = filepath.FromSlash(directory)
	if parsed.Host == "." {
		directory, err = filepath.Abs(filepath.FromSlash(strings.TrimPrefix(parsed.Path, "/")))
		if err != nil {
			return nil, err
		}
	}
	if filepath.Separator == '\\' && len(directory) >= 3 && directory[0] == '\\' && directory[2] == ':' {
		directory = directory[1:]
	}
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("file URL must specify an absolute directory")
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("open local directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local file URL is not a directory: %s", directory)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	return &directoryHandler{root: directoryFS{root: resolved}}, nil
}

type directoryFS struct{ root string }

func (d directoryFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") {
		return nil, fs.ErrPermission
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(d.root, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(d.root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fs.ErrPermission
	}
	return os.Open(resolved)
}

func (h *directoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "."
	}
	file, err := h.root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := file.Stat()
	file.Close()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			target := *r.URL
			target.Path += "/"
			http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
			return
		}
		for _, index := range []string{"index.html", "index.htm"} {
			indexName := path.Join(name, index)
			indexFile, err := h.root.Open(indexName)
			if err != nil {
				continue
			}
			indexInfo, statErr := indexFile.Stat()
			indexFile.Close()
			if statErr == nil && !indexInfo.IsDir() {
				http.ServeFileFS(w, r, h.root, filepath.ToSlash(indexName))
				return
			}
		}
	}
	http.FileServer(http.FS(h.root)).ServeHTTP(w, r)
}
