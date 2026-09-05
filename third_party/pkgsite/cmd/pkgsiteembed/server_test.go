package pkgsiteembed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/pkgsite/internal"
)

func TestExcludeModule(t *testing.T) {
	t.Setenv("GOWORK", "off")
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":            "module example.com/hidden\n\ngo 1.26\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ./dependency\n",
		"hidden.go":         "package hidden\nimport _ \"example.com/dependency\"\n",
		"dependency/go.mod": "module example.com/dependency\n\ngo 1.26\n",
		"dependency/dep.go": "package dependency\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	exclude := true
	handler, err := NewHandler(t.Context(), Config{
		Paths: []string{dir}, Offline: true, ListModules: true,
		ProxyHandler: http.NotFoundHandler(),
		ProxyModules: []string{"example.com/proxyhidden", "example.com/visible"},
		SearchPackages: []SearchPackage{
			{Name: "proxyhidden", Path: "example.com/proxyhidden", ModulePath: "example.com/proxyhidden"},
			{Name: "visible", Path: "example.com/visible", ModulePath: "example.com/visible"},
		},
		ExcludeModule: func(m string) bool {
			return exclude && (m == "example.com/hidden" || m == "example.com/dependency" || m == "example.com/proxyhidden")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	exclude = false
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/search?q=example.com", nil))
	for _, modulePath := range []string{"example.com/hidden", "example.com/dependency", "example.com/proxyhidden"} {
		if !strings.Contains(recorder.Body.String(), modulePath) {
			t.Fatalf("unfiltered search missing %s", modulePath)
		}
	}
	exclude = true
	for _, path := range []string{"/", "/search?q=example.com"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, recorder.Code)
		}
		body := recorder.Body.String()
		for _, hidden := range []string{"example.com/hidden", "example.com/dependency", "example.com/proxyhidden"} {
			if strings.Contains(body, hidden) {
				t.Errorf("GET %s contains excluded module %s", path, hidden)
			}
		}
		if !strings.Contains(body, "example.com/visible") {
			t.Errorf("GET %s missing visible module", path)
		}
	}
	for _, path := range []string{"/example.com/hidden", "/example.com/dependency"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("direct GET %s: status %d", path, recorder.Code)
		}
	}
}

func TestSearchableProxyGetterSearch(t *testing.T) {
	published := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	getter := &searchableProxyGetter{packages: []SearchPackage{
		{Name: "goldmark", Path: "github.com/yuin/goldmark", ModulePath: "github.com/yuin/goldmark", Version: "v1.6.0", CommitTime: published, Licenses: []string{"MIT"}, ImportedBy: []string{"example.com/markdown"}, NumImportedBy: 1},
		{Name: "parser", Path: "github.com/yuin/goldmark/parser", ModulePath: "github.com/yuin/goldmark", Version: "v1.6.0"},
		{Name: "goldmark", Path: "example.com/goldmark", ModulePath: "example.com/goldmark", Version: "v1.0.0", Synopsis: "Package goldmark renders Markdown."},
	}}

	results, err := getter.Search(context.Background(), "yuin goldmark", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resultPaths(results), []string{"github.com/yuin/goldmark", "github.com/yuin/goldmark/parser"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("search paths = %v, want %v", got, want)
	}
	if results[0].Version != "v1.6.0" || results[0].ModulePath != "github.com/yuin/goldmark" ||
		!results[0].CommitTime.Equal(published) || !reflect.DeepEqual(results[0].Licenses, []string{"MIT"}) ||
		results[0].NumImportedBy != 1 {
		t.Fatalf("first result = %+v", results[0])
	}
	if got, want := getter.ImportedBy("github.com/yuin/goldmark"), []string{"example.com/markdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ImportedBy() = %v, want %v", got, want)
	}

	results, err = getter.Search(context.Background(), "goldmark", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].PackagePath != "example.com/goldmark" {
		t.Fatalf("limited exact-name search = %+v", results)
	}

	results, err = getter.Search(context.Background(), "renders Markdown", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resultPaths(results), []string{"example.com/goldmark"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("synopsis search paths = %v, want %v", got, want)
	}
}

func TestSearchExcludesInternalPackages(t *testing.T) {
	getter := &searchableProxyGetter{packages: []SearchPackage{
		{Name: "internal", Path: "internal"},
		{Name: "hidden", Path: "internal/hidden"},
		{Name: "internal", Path: "example.com/mod/internal"},
		{Name: "hidden", Path: "example.com/mod/internal/hidden"},
		{Name: "internaltools", Path: "example.com/mod/internaltools"},
	}}
	results, err := getter.Search(t.Context(), "internal", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resultPaths(results), []string{"example.com/mod/internaltools"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("search paths = %v, want %v", got, want)
	}
}

func TestOfflineModuleGetterSourceInfo(t *testing.T) {
	tests := []struct {
		name       string
		modulePath string
		wantRepo   string
		wantInfo   bool
	}{
		{
			name:       "generic VCS repository with module subpath",
			modulePath: "git.example.com/group/test.git/subpath",
			wantRepo:   "https://git.example.com/group/test",
			wantInfo:   true,
		},
		{
			name:       "custom GitLab repository with module subpath",
			modulePath: "gitlab.example.com/group/test.git/subpath",
			wantRepo:   "https://gitlab.example.com/group/test",
			wantInfo:   true,
		},
		{
			name:       "unrecognized vanity path",
			modulePath: "code.example.com/group/test/subpath",
		},
	}

	getter := &offlineModuleGetter{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info, err := getter.SourceInfo(context.Background(), test.modulePath, "v1.2.3")
			if err != nil {
				t.Fatal(err)
			}
			if (info != nil) != test.wantInfo {
				t.Fatalf("SourceInfo() = %v, want info %t", info, test.wantInfo)
			}
			if info != nil && info.RepoURL() != test.wantRepo {
				t.Fatalf("RepoURL() = %q, want %q", info.RepoURL(), test.wantRepo)
			}
		})
	}
}

func resultPaths(results []*internal.SearchResult) []string {
	paths := make([]string, len(results))
	for i, result := range results {
		paths[i] = result.PackagePath
	}
	return paths
}
