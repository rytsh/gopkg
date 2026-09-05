package fetchdatasource

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/fetch"
	"golang.org/x/pkgsite/internal/proxy"
	"golang.org/x/pkgsite/internal/source"
)

func TestGetUnitPopulatesImportCountsForMain(t *testing.T) {
	const (
		modulePath = "example.com/mod"
		version    = "v1.2.3"
	)
	getter := &importedByTestGetter{
		content: fstest.MapFS{
			"go.mod": &fstest.MapFile{Data: []byte("module " + modulePath + "\n\ngo 1.26\n")},
			"mod.go": &fstest.MapFile{Data: []byte("package mod\nimport (\n\"fmt\"\n\"strings\"\n)\nfunc Hello() string { return fmt.Sprint(strings.ToUpper(\"hello\")) }\n")},
		},
		importers: map[string][]string{
			modulePath: {"example.com/first", "example.com/second"},
		},
	}
	ds := Options{
		Getters:            []fetch.ModuleGetter{getter},
		BypassLicenseCheck: true,
		ExcludeModule:      func(string) bool { return true },
	}.New()

	um, err := ds.GetUnitMeta(t.Context(), modulePath, modulePath, version)
	if err != nil {
		t.Fatal(err)
	}
	withoutMain, err := ds.GetUnit(t.Context(), um, 0, internal.BuildContext{})
	if err != nil {
		t.Fatal(err)
	}
	if withoutMain.NumImportedBy != 0 {
		t.Fatalf("GetUnit() without WithMain NumImportedBy = %d, want 0", withoutMain.NumImportedBy)
	}
	withMain, err := ds.GetUnit(t.Context(), um, internal.WithMain, internal.BuildContext{})
	if err != nil {
		t.Fatal(err)
	}
	if withMain.NumImportedBy != 2 {
		t.Fatalf("GetUnit() with WithMain NumImportedBy = %d, want 2", withMain.NumImportedBy)
	}
	if withMain.NumImports != 2 || withMain.NumImports != len(withMain.Imports) {
		t.Fatalf("GetUnit() NumImports = %d, Imports = %v, want 2 imports", withMain.NumImports, withMain.Imports)
	}
}

func TestSearchExcludeModule(t *testing.T) {
	getter := &searchTestGetter{results: []*internal.SearchResult{
		{PackagePath: "example.com/hidden/a", ModulePath: "example.com/hidden", Score: 5},
		{PackagePath: "example.com/hidden/b", ModulePath: "example.com/hidden", Score: 4},
		{PackagePath: "example.com/visible/a", ModulePath: "example.com/visible", Score: 3},
		{PackagePath: "example.com/visible/b", ModulePath: "example.com/visible", Score: 2},
	}}
	for _, test := range []struct {
		name    string
		exclude func(string) bool
		offset  int
		want    []string
	}{
		{"nil", nil, 0, []string{"example.com/hidden/a", "example.com/hidden/b"}},
		{"filtered", func(m string) bool { return m == "example.com/hidden" }, 0, []string{"example.com/visible/a", "example.com/visible/b"}},
		{"pagination", func(m string) bool { return m == "example.com/hidden" }, 1, []string{"example.com/visible/b"}},
		{"all excluded", func(string) bool { return true }, 0, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			ds := Options{Getters: []fetch.ModuleGetter{getter}, ExcludeModule: test.exclude}.New()
			rs, err := ds.Search(t.Context(), "query", internal.SearchOptions{Offset: test.offset, MaxResults: 2})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range rs {
				got = append(got, r.PackagePath)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("paths = %v, want %v", got, test.want)
			}
		})
	}
	getter.err = errors.New("search failed")
	ds := Options{Getters: []fetch.ModuleGetter{getter}, ExcludeModule: func(string) bool { return true }}.New()
	if _, err := ds.Search(t.Context(), "query", internal.SearchOptions{MaxResults: 1}); !errors.Is(err, getter.err) {
		t.Fatalf("Search error = %v, want %v", err, getter.err)
	}
}

type searchTestGetter struct {
	fetch.ModuleGetter
	results []*internal.SearchResult
	err     error
}

func (g *searchTestGetter) Search(_ context.Context, _ string, limit int) ([]*internal.SearchResult, error) {
	return g.results[:min(limit, len(g.results))], g.err
}

type importedByTestGetter struct {
	fetch.ModuleGetter
	content   fstest.MapFS
	importers map[string][]string
}

func (g *importedByTestGetter) Info(context.Context, string, string) (*proxy.VersionInfo, error) {
	return &proxy.VersionInfo{Version: "v1.2.3", Time: time.Time{}}, nil
}

func (g *importedByTestGetter) Mod(context.Context, string, string) ([]byte, error) {
	return fs.ReadFile(g.content, "go.mod")
}

func (g *importedByTestGetter) ContentDir(context.Context, string, string) (fs.FS, error) {
	return g.content, nil
}

func (g *importedByTestGetter) SourceInfo(context.Context, string, string) (*source.Info, error) {
	return nil, nil
}

func (g *importedByTestGetter) ImportedBy(packagePath string) []string {
	return g.importers[packagePath]
}
