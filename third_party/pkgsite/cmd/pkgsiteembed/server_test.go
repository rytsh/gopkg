package pkgsiteembed

import (
	"context"
	"reflect"
	"testing"
	"time"

	"golang.org/x/pkgsite/internal"
)

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

func resultPaths(results []*internal.SearchResult) []string {
	paths := make([]string, len(results))
	for i, result := range results {
		paths[i] = result.PackagePath
	}
	return paths
}
