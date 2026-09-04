package modproxy

import (
	"archive/zip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/module"
)

func TestStoreServesAthensAndStandardProxy(t *testing.T) {
	root := t.TempDir()
	athensDir := filepath.Join(root, "example.com", "athens", "v1.2.3")
	writeFile(t, filepath.Join(athensDir, "go.mod"), "module example.com/athens\n\ngo 1.26\n")
	writeZip(t, filepath.Join(athensDir, "source.zip"), map[string]string{
		"example.com/athens@v1.2.3/athens.go": "package athens\n",
	})

	standardPath := "example.com/Acme/tool"
	escapedPath, err := module.EscapePath(standardPath)
	if err != nil {
		t.Fatal(err)
	}
	standardBase := filepath.Join(root, filepath.FromSlash(escapedPath), "@v", "v0.4.0")
	writeFile(t, standardBase+".info", `{"Version":"v0.4.0","Time":"2026-01-02T03:04:05Z"}`)
	writeFile(t, standardBase+".mod", "module "+standardPath+"\n\ngo 1.26\n")
	writeZip(t, standardBase+".zip", map[string]string{
		escapedPath + "@v0.4.0/tool.go": "package tool\n",
	})

	store, err := Open([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := store.Modules(), []string{standardPath, "example.com/athens"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Modules() = %v, want %v", got, want)
	}

	server := httptest.NewServer(store)
	defer server.Close()
	checks := []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/example.com/athens/@v/list", "text/plain", "v1.2.3"},
		{"/example.com/athens/@latest", "application/json", `"Version":"v1.2.3"`},
		{"/example.com/athens/@v/v1.2.3.mod", "text/plain", "module example.com/athens"},
		{"/example.com/athens/@v/v1.2.3.zip", "application/zip", ""},
		{"/" + escapedPath + "/@v/v0.4.0.info", "application/json", `"Version":"v0.4.0"`},
	}
	for _, check := range checks {
		response, err := http.Get(server.URL + check.path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", check.path, response.StatusCode)
		}
		if !strings.HasPrefix(response.Header.Get("Content-Type"), check.contentType) {
			t.Errorf("GET %s content type = %q, want prefix %q", check.path, response.Header.Get("Content-Type"), check.contentType)
		}
		if check.contains != "" && !strings.Contains(string(body), check.contains) {
			t.Errorf("GET %s body = %q, want content %q", check.path, body, check.contains)
		}
	}

	request, err := http.NewRequest(http.MethodHead, server.URL+"/example.com/athens/@v/v1.2.3.zip", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength <= 0 {
		t.Fatalf("HEAD zip status/length = %d/%d, want 200 and positive length", response.StatusCode, response.ContentLength)
	}
}

func TestStoreUsesProxyLatestSemanticsAndIndexesLatestPackages(t *testing.T) {
	root := t.TempDir()
	modulePath := "example.com/releases"
	pseudoModule := "example.com/pseudo"
	writeAthensVersion(t, root, modulePath, "v1.2.0", "2025-01-01T00:00:00Z", "stable", pseudoModule+"/newer")
	writeAthensVersion(t, root, modulePath, "v1.3.0-rc.1", "2026-01-01T00:00:00Z", "candidate")
	writeAthensVersion(t, root, modulePath, "v1.3.0-0.20260202000000-aaaaaaaaaaaa", "2026-02-02T00:00:00Z", "pseudo")

	writeAthensVersion(t, root, pseudoModule, "v0.0.0-20260101000000-aaaaaaaaaaaa", "2025-01-01T00:00:00Z", "older")
	writeAthensVersion(t, root, pseudoModule, "v0.0.0-20250101000000-bbbbbbbbbbbb", "2026-01-01T00:00:00Z", "newer")

	store, err := Open([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(store)
	defer server.Close()

	assertResponseContains(t, server.URL+"/"+modulePath+"/@latest", `"Version":"v1.2.0"`)
	assertResponseContains(t, server.URL+"/"+pseudoModule+"/@latest", `"Version":"v0.0.0-20250101000000-bbbbbbbbbbbb"`)
	response := assertResponseContains(t, server.URL+"/"+modulePath+"/@v/list", "v1.3.0-rc.1")
	if strings.Contains(response, "20260202000000") {
		t.Fatalf("version list contains pseudo-version: %q", response)
	}

	got := store.Packages()
	want := []Package{
		{Name: "stable", Path: modulePath + "/stable", ModulePath: modulePath, Version: "v1.2.0", Synopsis: "Package stable provides local documentation.", CommitTime: mustTime(t, "2025-01-01T00:00:00Z"), Licenses: []string{"MIT"}},
		{Name: "newer", Path: pseudoModule + "/newer", ModulePath: pseudoModule, Version: "v0.0.0-20250101000000-bbbbbbbbbbbb", Synopsis: "Package newer provides local documentation.", CommitTime: mustTime(t, "2026-01-01T00:00:00Z"), Licenses: []string{"MIT"}, ImportedBy: []string{modulePath + "/stable"}, NumImportedBy: 1},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Packages() = %#v, want %#v", got, want)
	}
}

func assertResponseContains(t *testing.T, url, want string) string {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
		t.Fatalf("GET %s = %d %q, want status 200 containing %q", url, response.StatusCode, body, want)
	}
	return string(body)
}

func TestAddVersionPublishesValidatedProxyVersion(t *testing.T) {
	root := t.TempDir()
	modulePath := "example.com/uploaded"
	version := "v1.2.3"
	sourceZip := filepath.Join(t.TempDir(), "module.zip")
	writeZip(t, sourceZip, map[string]string{
		modulePath + "@" + version + "/uploaded.go": "package uploaded\n",
	})
	zipFile, err := os.Open(sourceZip)
	if err != nil {
		t.Fatal(err)
	}
	defer zipFile.Close()

	before, err := Fingerprint([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	err = AddVersion(
		root,
		modulePath,
		version,
		strings.NewReader(`{"Version":"v1.2.3","Time":"2026-01-02T03:04:05Z"}`),
		strings.NewReader("module "+modulePath+"\n\ngo 1.27\n"),
		zipFile,
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("Fingerprint did not change after adding a version")
	}
	store, err := Open([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := store.Modules(), []string{modulePath}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Modules() = %v, want %v", got, want)
	}
	list, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(modulePath), "@v", "list"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(list); got != version+"\n" {
		t.Fatalf("version list = %q, want %q", got, version+"\n")
	}
}

func TestDiscoverLocal(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "nested", "second")
	writeFile(t, filepath.Join(first, "go.mod"), "module example.com/first\n")
	writeFile(t, filepath.Join(second, "go.mod"), "module example.com/second\n")
	writeFile(t, filepath.Join(root, "vendor", "ignored", "go.mod"), "module example.com/ignored\n")
	writeFile(t, filepath.Join(root, "third_party", "ignored", "go.mod"), "module example.com/ignored-too\n")

	got, err := DiscoverLocal([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{first, second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiscoverLocal() = %v, want %v", got, want)
	}
}

func writeAthensVersion(t *testing.T, root, modulePath, version, timestamp, packageDir string, imports ...string) {
	t.Helper()
	versionDir := filepath.Join(root, filepath.FromSlash(modulePath), version)
	writeFile(t, filepath.Join(versionDir, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
	writeFile(t, filepath.Join(versionDir, version+".info"), `{"Version":"`+version+`","Time":"`+timestamp+`"}`)
	source := "// Package " + packageDir + " provides local documentation.\npackage " + packageDir + "\n"
	for _, imported := range imports {
		source += "import _ " + strconv.Quote(imported) + "\n"
	}
	writeZip(t, filepath.Join(versionDir, "source.zip"), map[string]string{
		modulePath + "@" + version + "/" + packageDir + "/" + packageDir + ".go": source,
		modulePath + "@" + version + "/LICENSE":                                  mitLicense,
	})
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, name string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(output)
	for fileName, content := range files {
		file, err := archive.Create(fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

const mitLicense = `MIT License

Copyright (c) 2026

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.`
