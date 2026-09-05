package fetchdatasource

import (
	"context"
	"io/fs"
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
