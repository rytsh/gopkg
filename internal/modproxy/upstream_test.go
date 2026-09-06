package modproxy

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

func TestUpstreamWarm(t *testing.T) {
	for _, test := range []struct {
		name, separator string
		status          int
		wantRequests    int64
	}{
		{"comma stops on 500", ",", 500, 0},
		{"comma falls back on 404", ",", 404, 3},
		{"comma falls back on 410", ",", 410, 3},
		{"pipe falls back on 500", "|", 500, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "failed", test.status)
			}))
			defer first.Close()
			var requests atomic.Int64
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := requests.Add(1) - 1
				if user, password, ok := r.BasicAuth(); !ok || user != "user" || password != "secret" {
					t.Error("warm did not preserve authentication")
				}
				exts := []string{".info", ".mod", ".zip"}
				if index >= 3 || r.Method != http.MethodGet || r.URL.Path != "/cache/example.com/!acme/tool/@v/v1.0.0"+exts[index] {
					t.Errorf("unexpected warm request: %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", 64<<10)))
			}))
			defer second.Close()
			u, err := NewUpstream(first.URL+test.separator+strings.Replace(second.URL, "://", "://user:secret@", 1)+"/cache", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			err = u.Warm(t.Context(), "example.com/Acme/tool", "v1.0.0")
			if (err == nil) != (test.wantRequests == 3) || requests.Load() != test.wantRequests {
				t.Fatalf("Warm error=%v requests=%d", err, requests.Load())
			}
			if err := u.Warm(t.Context(), "../invalid", "v1.0.0"); err == nil {
				t.Fatal("invalid module accepted")
			}
		})
	}
}

func TestUpstreamWarmConsumesZipBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			w.Header().Set("Content-Length", "1000")
		}
		_, _ = w.Write([]byte("short body"))
	}))
	defer server.Close()
	u, err := NewUpstream(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Warm(t.Context(), "example.com/tool", "v1.0.0"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Warm error = %v, want truncated zip body error", err)
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
