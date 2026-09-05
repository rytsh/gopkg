package frontend

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/google/safehtml/template"
	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/source"
	"golang.org/x/pkgsite/internal/testing/fakedatasource"
)

func TestMainDetailsModFileURL(t *testing.T) {
	generic, err := source.ModuleInfo(t.Context(), nil, "git.example.com/group/repo.git/submodule", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if generic == nil || generic.RepoURL() != "https://git.example.com/group/repo" {
		t.Fatalf("generic source info = %v, want repository without file templates", generic)
	}
	tmpl, err := template.New("meta").Funcs(template.FuncMap{
		"stripscheme": func(s string) string { return s },
	}).ParseFiles("../../static/frontend/unit/main/_meta.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		modulePath string
		info       *source.Info
		wantURL    string
	}{
		{"nil source", "example.com/repo", nil, ""},
		{"generic git", "git.example.com/group/repo.git/submodule", generic, ""},
		{"GitHub", "github.com/owner/repo", source.NewGitHubInfo("https://github.com/owner/repo", "", "v1.2.3"), "https://github.com/owner/repo/blob/v1.2.3/go.mod"},
		{"GitHub submodule", "github.com/owner/repo/submodule", source.NewGitHubInfo("https://github.com/owner/repo", "submodule", "submodule/v1.2.3"), "https://github.com/owner/repo/blob/submodule/v1.2.3/submodule/go.mod"},
		{"local files", "example.com/repo/submodule", source.FilesInfo("local/repo/submodule"), "/files/local/repo/submodule/go.mod"},
	} {
		for _, hasGoMod := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/HasGoMod=%t", test.name, hasGoMod), func(t *testing.T) {
				um := internal.UnitMeta{
					Path: test.modulePath + "/pkg/nested",
					Name: "nested",
					ModuleInfo: internal.ModuleInfo{
						ModulePath: test.modulePath,
						Version:    "v1.2.3",
						SourceInfo: test.info,
						HasGoMod:   hasGoMod,
					},
				}
				ds := fakedatasource.New()
				ds.MustInsertModule(t, &internal.Module{
					ModuleInfo: um.ModuleInfo,
					Units:      []*internal.Unit{{UnitMeta: um}},
				})
				details, err := fetchMainDetails(t.Context(), ds, &um, um.Version, false, internal.BuildContext{})
				if err != nil {
					t.Fatal(err)
				}
				if details.ModFileURL != test.wantURL {
					t.Errorf("ModFileURL = %q, want %q", details.ModFileURL, test.wantURL)
				}
				var out bytes.Buffer
				if err := tmpl.ExecuteTemplate(&out, "unit-meta-details", struct {
					Unit    *internal.UnitMeta
					Details *MainDetails
				}{&um, details}); err != nil {
					t.Fatal(err)
				}
				summary, _, _ := strings.Cut(out.String(), "</summary>")
				wantLabel := "Valid go.mod file"
				wantLinks := 0
				if hasGoMod && test.wantURL != "" {
					wantLabel = `Valid <a href="` + test.wantURL + `" target="_blank" rel="noopener">go.mod</a> file`
					wantLinks = 1
				}
				if !strings.Contains(summary, wantLabel) || strings.Count(summary, "<a ") != wantLinks {
					t.Errorf("go.mod summary = %s, want %q and %d links", summary, wantLabel, wantLinks)
				}
				wantStatus := `alt="unchecked"`
				if hasGoMod {
					wantStatus = `alt="checked"`
				}
				if !strings.Contains(summary, wantStatus) {
					t.Errorf("go.mod summary = %s, want status %s", summary, wantStatus)
				}
			})
		}
	}
}
