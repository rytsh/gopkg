package site

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rytsh/gopkg/internal/modproxy"
)

func fetchRequest(target string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/-/fetch", strings.NewReader(url.Values{"path": {target}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestMissingVersionFetch(t *testing.T) {
	const modulePath = "github.com/worldline-go/wkafka"
	const version = "v0.6.7"
	const target = "/" + modulePath + "@" + version + "/sub"
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for name, content := range map[string]string{
		"go.mod":     "module " + modulePath + "\n\ngo 1.23\n",
		"sub/sub.go": "// Package sub documents the fetched package.\npackage sub\n\n// Hello says hello.\nfunc Hello() {}\n",
	} {
		f, err := zw.Create(modulePath + "@" + version + "/" + name)
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
	var requests atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if user, password, ok := r.BasicAuth(); !ok || user != "proxyuser" || password != "proxysecret" {
			t.Error("configured proxy credentials were not used")
		}
		switch r.URL.Path {
		case "/cache/" + modulePath + "/@v/" + version + ".info":
			_, _ = w.Write([]byte(`{"Version":"v0.6.7","Time":"2026-01-02T03:04:05Z"}`))
		case "/cache/" + modulePath + "/@v/" + version + ".mod":
			_, _ = w.Write([]byte("module " + modulePath + "\n\ngo 1.23\n"))
		case "/cache/" + modulePath + "/@v/" + version + ".zip":
			_, _ = w.Write(archive.Bytes())
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	manager, err := New(t.Context(), Config{
		ProxyDirs:     []string{t.TempDir()},
		UpstreamProxy: strings.Replace(upstream.URL, "://", "://proxyuser:proxysecret@", 1) + "/cache",
		FetchTimeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	css := httptest.NewRecorder()
	manager.ServeHTTP(css, httptest.NewRequest(http.MethodGet, "/static/shared/color/color.css", nil))
	if css.Code != http.StatusOK || !strings.Contains(css.Body.String(), "--color-text") {
		t.Fatalf("missing-page stylesheet unavailable: status=%d", css.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		manager.ServeHTTP(w, httptest.NewRequest(method, target, nil))
		if w.Code != http.StatusNotFound || requests.Load() != 0 {
			t.Fatalf("%s: status=%d, upstream requests=%d", method, w.Code, requests.Load())
		}
		if method == http.MethodGet && (!strings.Contains(w.Body.String(), `action="/-/fetch"`) || !strings.Contains(w.Body.String(), ">Fetch</button>")) {
			t.Fatalf("missing Fetch form: %s", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "proxysecret") || strings.Contains(w.Body.String(), upstream.URL) {
			t.Fatal("proxy configuration leaked into page")
		}
		if method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
			t.Fatal("missing-page cache or form security headers missing")
		}
	}
	for range 2 {
		w := httptest.NewRecorder()
		manager.Fetch(w, fetchRequest(target))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != target {
			t.Fatalf("fetch: status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
		}
	}
	if requests.Load() != 3 || !manager.current.Load().store.HasVersion(modulePath, version) || manager.Stats().ProxyModules != 1 {
		t.Fatalf("version not published/indexed or refetched: requests=%d stats=%+v", requests.Load(), manager.Stats())
	}
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Hello") || requests.Load() != 3 {
		t.Fatalf("documentation after fetch: status=%d requests=%d body=%s", w.Code, requests.Load(), w.Body.String())
	}
}

func TestFetchFailures(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "upstream-secret", status)
			}))
			defer upstream.Close()
			u, err := modproxy.NewUpstream(upstream.URL, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			manager := &Manager{upstream: u, fetchFailures: make(map[string]fetchFailure)}
			want := http.StatusBadGateway
			if status == http.StatusNotFound || status == http.StatusGone {
				want = http.StatusNotFound
			}
			for range 2 {
				w := httptest.NewRecorder()
				manager.Fetch(w, fetchRequest("/example.com/mod@v1.0.0"))
				if w.Code != want || !strings.Contains(w.Body.String(), `role="alert"`) || strings.Contains(w.Body.String(), "upstream-secret") {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			}
			if requests.Load() != 1 {
				t.Fatalf("negative cache did not suppress retry: %d requests", requests.Load())
			}
		})
	}
}

func TestFetchDisabledAndInvalidRequests(t *testing.T) {
	manager := &Manager{}
	manager.current.Store(&snapshot{handler: http.NotFoundHandler()})
	w := httptest.NewRecorder()
	manager.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/example.com/mod@v1.0.0", nil))
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "<form") || !strings.Contains(w.Body.String(), "fetching is disabled") {
		t.Fatalf("disabled page: status=%d body=%s", w.Code, w.Body.String())
	}
	for _, test := range []struct {
		name    string
		request *http.Request
		status  int
	}{
		{"disabled", fetchRequest("/example.com/mod@v1.0.0"), http.StatusServiceUnavailable},
		{"GET", httptest.NewRequest(http.MethodGet, "/-/fetch", nil), http.StatusMethodNotAllowed},
		{"latest", fetchRequest("/example.com/mod@latest"), http.StatusBadRequest},
		{"redirect", fetchRequest("//example.com/mod@v1.0.0"), http.StatusBadRequest},
		{"traversal", fetchRequest("/example.com/mod@v1.0.0/../bad"), http.StatusBadRequest},
		{"XSS", fetchRequest(`/example.com/mod@v1.0.0/<script>`), http.StatusBadRequest},
		{"oversized", fetchRequest(strings.Repeat("x", 9<<10)), http.StatusBadRequest},
		{"query only", httptest.NewRequest(http.MethodPost, "/-/fetch?path=/example.com/mod@v1.0.0", nil), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			manager.Fetch(w, test.request)
			if w.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.status, w.Body.String())
			}
		})
	}
}
