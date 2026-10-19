package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type collectorHandler struct{ root string }

var copyFile = io.Copy

func NewCollectorHandler(root string) (http.Handler, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("collect path must be an existing directory: %s", root)
	}
	return &collectorHandler{root: root}, nil
}

func (h *collectorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" || r.URL.Path == "/index.html" || strings.HasPrefix(r.URL.Path, "/__simple_ng_internal__/tunnel/"):
		serveTunnelUI(w, r)
	case r.URL.Path == "/api/upload" && r.Method == http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
		if err := r.ParseMultipartForm(512 << 20); err != nil {
			http.Error(w, "Upload rejected", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
		if name == "." || name == "" {
			http.Error(w, "Invalid filename", http.StatusBadRequest)
			return
		}
		out, err := os.OpenFile(filepath.Join(h.root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			http.Error(w, "A file with that name already exists", http.StatusConflict)
			return
		}
		_, copyErr := copyFile(out, file)
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			os.Remove(filepath.Join(h.root, name))
			http.Error(w, "Could not store upload", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case r.URL.Path == "/api/files" && r.Method == http.MethodGet:
		entries, err := os.ReadDir(h.root)
		if err != nil {
			http.Error(w, "Could not list files", http.StatusInternalServerError)
			return
		}
		files := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				files = append(files, entry.Name())
			}
		}
		sort.Strings(files)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]string{"files": files})
	case strings.HasPrefix(r.URL.Path, "/api/files/") && r.Method == http.MethodGet:
		name := strings.TrimPrefix(r.URL.Path, "/api/files/")
		if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "\\:") {
			http.NotFound(w, r)
			return
		}
		file, err := os.Open(filepath.Join(h.root, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, name, info.ModTime(), file)
	default:
		http.NotFound(w, r)
	}
}
