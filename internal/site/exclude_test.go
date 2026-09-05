package site

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rytsh/gopkg/internal/modproxy"
)

func TestExcludeModule(t *testing.T) {
	m := Manager{config: Config{Exclude: []string{"balbbla.com/**", "github.com/acme/legacy", "example.com/*"}}}
	for path, want := range map[string]bool{
		"balbbla.com/repo": true, "balbbla.com/team/repo/v2": true,
		"otherbalbbla.com/repo": false, "github.com/acme/legacy": true,
		"github.com/acme/legacy-next": false, "example.com/repo": true,
		"example.com/team/repo": false,
	} {
		if got := m.excludeModule(path); got != want {
			t.Errorf("excludeModule(%q) = %v, want %v", path, got, want)
		}
	}
	if _, err := New(t.Context(), Config{Exclude: []string{"[invalid"}}); err == nil || !strings.Contains(err.Error(), "invalid exclude pattern") {
		t.Fatalf("invalid pattern error = %v", err)
	}
}

func TestExcludeProxyModules(t *testing.T) {
	root := t.TempDir()
	for _, modulePath := range []string{"balbbla.com/team/tool", "example.com/tool"} {
		mod := "module " + modulePath + "\n\ngo 1.23\n"
		var archive bytes.Buffer
		zw := zip.NewWriter(&archive)
		for name, content := range map[string]string{
			"go.mod": mod, "tool.go": "// Package tool provides tools.\npackage tool\n",
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
		if err := modproxy.AddVersion(root, modulePath, "v1.0.0", strings.NewReader(`{"Version":"v1.0.0","Time":"2026-01-02T03:04:05Z"}`), strings.NewReader(mod), bytes.NewReader(archive.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	for _, pattern := range []string{"balbbla.com/**", "**"} {
		t.Run(pattern, func(t *testing.T) {
			patterns := []string{pattern}
			manager, err := New(t.Context(), Config{ProxyDirs: []string{root}, Exclude: patterns})
			if err != nil {
				t.Fatal(err)
			}
			patterns[0] = "changed.example/**"
			for range 2 {
				wantModules, wantPackages := 1, 2
				if pattern == "**" {
					wantModules, wantPackages = 0, 0
				}
				if stats := manager.Stats(); stats.ProxyModules != wantModules || stats.SearchPackages != wantPackages {
					t.Fatalf("unexpected stats: %+v", stats)
				}
				for _, target := range []string{"/", "/search?q=tool"} {
					w := httptest.NewRecorder()
					manager.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
					if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "balbbla.com/team/tool") {
						t.Errorf("GET %s: status=%d, excluded module visible=%v", target, w.Code, strings.Contains(w.Body.String(), "balbbla.com/team/tool"))
					}
					if pattern != "**" && !strings.Contains(w.Body.String(), "example.com/tool") {
						t.Errorf("GET %s: visible module missing", target)
					}
				}
				if _, err := manager.Reload(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
