package fetch

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestSearchExcludesInternalPackages(t *testing.T) {
	getter := &goPackagesModuleGetter{packages: []*packages.Package{
		{PkgPath: "internal"},
		{PkgPath: "internal/hidden"},
		{PkgPath: "example.com/mod/internal"},
		{PkgPath: "example.com/mod/internal/hidden"},
		{PkgPath: "example.com/mod/internaltools"},
	}}
	results, err := getter.Search(t.Context(), "internal", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].PackagePath != "example.com/mod/internaltools" {
		t.Fatalf("search results = %+v, want only internaltools", results)
	}
}
