package site

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAthensModulePathMismatchPage(t *testing.T) {
	const modulePath = "example.com/tool.git"
	for _, declared := range []string{"example.com/tool", modulePath} {
		t.Run(declared, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, modulePath, "v1.0.0")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			mod := "module " + declared + "\n\ngo 1.23\n"
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			for name, content := range map[string]string{
				"go.mod":     mod,
				"tool.go":    "// Package tool provides tools.\npackage tool\n",
				"sub/sub.go": "// Package sub provides tools.\npackage sub\n",
			} {
				f, err := zw.Create(modulePath + "@v1.0.0/" + name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.Write([]byte(content)); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string][]byte{"go.mod": []byte(mod), "source.zip": archive.Bytes()} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := New(t.Context(), Config{ProxyDirs: []string{root}})
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"/" + modulePath, "/" + modulePath + "@v1.0.0", "/" + modulePath + "@v1.0.0/sub"} {
				w := httptest.NewRecorder()
				manager.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
				want := http.StatusOK
				if declared != modulePath {
					want = http.StatusUnprocessableEntity
				}
				if w.Code != want {
					t.Errorf("GET %s: status = %d, want %d", target, w.Code, want)
				}
				if declared != modulePath && (!strings.Contains(w.Body.String(), "go.mod") || !strings.Contains(w.Body.String(), "publish a new version")) {
					t.Errorf("GET %s: missing repair guidance", target)
				}
				if w.Header().Get("Location") != "" {
					t.Errorf("GET %s: unexpected redirect", target)
				}
			}
		})
	}
}
