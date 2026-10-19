package client

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDirectoryHandlerIndexesAssetsAndTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("html index"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.htm"), []byte("htm fallback"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "asset.txt"), []byte("asset"), 0600); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(root, "fallback")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "index.htm"), []byte("htm index"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.html")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	handler, err := NewDirectoryHandler(fileURL(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/", "html index"},
		{"/fallback/", "htm index"},
		{"/asset.txt", "asset"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != http.StatusOK || recorder.Body.String() != tc.want {
				t.Fatalf("status=%d body=%q, want %q", recorder.Code, recorder.Body.String(), tc.want)
			}
		})
	}
	for _, path := range []string{"/../outside.html", "/%2e%2e/outside.html"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code == http.StatusOK && strings.Contains(recorder.Body.String(), "secret") {
			t.Fatalf("path traversal exposed a file: %s", path)
		}
	}
}

func TestDirectoryHandlerPrefersIndexHTML(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.htm"), []byte("fallback"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDirectoryHandler(fileURL(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("preferred"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := NewDirectoryHandler(fileURL(root))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Body.String() != "preferred" {
		t.Fatalf("index.html did not take precedence: %q", recorder.Body.String())
	}
}

func TestDirectoryHandlerRejectsInvalidFileURLs(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "page.html")
	if err := os.WriteFile(file, []byte("page"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"http://example.com/site",
		"file://remote/share",
		"file:///?query=1",
		fileURL(file),
		"file:///does/not/exist",
	} {
		if _, err := NewDirectoryHandler(value); err == nil {
			t.Errorf("accepted invalid directory URL %q", value)
		}
	}
}

func fileURL(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(path) >= 2 && path[1] == ':' {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestDirectoryHandlerSymlinksStayWithinRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "public")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "private.txt")
	if err := os.WriteFile(outside, []byte("private content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	handler, err := NewDirectoryHandler(fileURL(root))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/escape.txt", nil))
	if recorder.Code == http.StatusOK || strings.Contains(recorder.Body.String(), "private content") {
		t.Fatalf("symlink escaped directory: %d %q", recorder.Code, recorder.Body.String())
	}
}
