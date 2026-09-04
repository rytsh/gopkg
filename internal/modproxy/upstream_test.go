package modproxy

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestUpstreamFetchesProxyArtifacts(t *testing.T) {
	modulePath := "example.com/Acme/tool"
	version := "v1.2.3"
	artifacts := proxyArtifacts(t, modulePath, version)
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		data, ok := artifacts[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	upstream, err := NewUpstream(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := upstream.Fetch(t.Context(), modulePath, version)
	if err != nil {
		t.Fatal(err)
	}
	defer fetched.Zip.Close()
	zipData, err := io.ReadAll(fetched.Zip)
	if err != nil {
		t.Fatal(err)
	}
	if len(zipData) == 0 || !bytes.Contains(fetched.Mod, []byte("module "+modulePath)) {
		t.Fatal("fetched artifacts are incomplete")
	}
	wantPaths := []string{
		"/example.com/!acme/tool/@v/v1.2.3.info",
		"/example.com/!acme/tool/@v/v1.2.3.mod",
		"/example.com/!acme/tool/@v/v1.2.3.zip",
	}
	if !reflect.DeepEqual(requested, wantPaths) {
		t.Fatalf("requested paths = %v, want %v", requested, wantPaths)
	}
}

func TestUpstreamHonorsGOPROXYFallbackSeparators(t *testing.T) {
	modulePath := "example.com/fallback"
	version := "v1.0.0"
	artifacts := proxyArtifacts(t, modulePath, version)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	defer first.Close()
	var secondRequests atomic.Int64
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondRequests.Add(1)
		data, ok := artifacts[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer second.Close()

	comma, err := NewUpstream(first.URL+","+second.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := comma.Fetch(t.Context(), modulePath, version); err == nil {
		t.Fatal("comma-separated GOPROXY unexpectedly fell back after HTTP 500")
	}
	if got := secondRequests.Load(); got != 0 {
		t.Fatalf("comma fallback sent %d requests to second proxy, want 0", got)
	}

	pipe, err := NewUpstream(first.URL+"|"+second.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := pipe.Fetch(t.Context(), modulePath, version)
	if err != nil {
		t.Fatal(err)
	}
	fetched.Zip.Close()
	if got := secondRequests.Load(); got != 3 {
		t.Fatalf("pipe fallback sent %d requests to second proxy, want 3", got)
	}
}

func proxyArtifacts(t *testing.T, modulePath, version string) map[string][]byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create(modulePath + "@" + version + "/tool.go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("package tool\n")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	prefix := "/" + modulePath + "/@v/" + version
	if modulePath == "example.com/Acme/tool" {
		prefix = "/example.com/!acme/tool/@v/" + version
	}
	return map[string][]byte{
		prefix + ".info": []byte(`{"Version":"` + version + `","Time":"2026-01-02T03:04:05Z"}`),
		prefix + ".mod":  []byte("module " + modulePath + "\n\ngo 1.27\n"),
		prefix + ".zip":  buffer.Bytes(),
	}
}
