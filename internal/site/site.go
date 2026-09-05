package site

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/rytsh/gopkg/internal/modproxy"
	"golang.org/x/mod/module"
	"golang.org/x/pkgsite/cmd/pkgsiteembed"
)

type Config struct {
	Paths         []string
	ProxyDirs     []string
	Exclude       []string
	UpstreamProxy string
	FetchTimeout  time.Duration
}

type Stats struct {
	LocalModules   int       `json:"local_modules"`
	ProxyModules   int       `json:"proxy_modules"`
	SearchPackages int       `json:"search_packages"`
	LoadedAt       time.Time `json:"loaded_at"`
}

type snapshot struct {
	handler     http.Handler
	stats       Stats
	fingerprint uint64
	store       *modproxy.Store
}

type Manager struct {
	config        Config
	upstream      *modproxy.Upstream
	current       atomic.Pointer[snapshot]
	mu            sync.Mutex
	fetchFailures map[string]fetchFailure
}

type fetchFailure struct {
	err       error
	expiresAt time.Time
}

func New(ctx context.Context, cfg Config) (*Manager, error) {
	for _, pattern := range cfg.Exclude {
		if !doublestar.ValidatePattern(pattern) {
			return nil, fmt.Errorf("invalid exclude pattern %q", pattern)
		}
	}
	var upstream *modproxy.Upstream
	if cfg.UpstreamProxy != "" {
		if len(cfg.ProxyDirs) == 0 {
			return nil, errors.New("on-demand fetch requires a writable proxy directory")
		}
		var err error
		upstream, err = modproxy.NewUpstream(cfg.UpstreamProxy, cfg.FetchTimeout)
		if err != nil {
			return nil, fmt.Errorf("configure upstream GOPROXY: %w", err)
		}
	}
	manager := &Manager{
		config: Config{
			Paths:     append([]string(nil), cfg.Paths...),
			ProxyDirs: append([]string(nil), cfg.ProxyDirs...),
			Exclude:   append([]string(nil), cfg.Exclude...),
		},
		upstream:      upstream,
		fetchFailures: make(map[string]fetchFailure),
	}
	if _, err := manager.Reload(ctx); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	current := m.current.Load()
	if current == nil {
		http.Error(w, "site is not ready", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		modulePath, version, ok := moduleVersionFromPath(r.URL.Path)
		if ok && (current.store == nil || !current.store.HasVersion(modulePath, version)) {
			m.serveMissing(w, r, r.URL.Path, modulePath, version, "", http.StatusNotFound)
			return
		}
	}
	current.handler.ServeHTTP(w, r)
}

// Fetch serves the explicit mutation action; the server applies admin authentication
// and cross-origin protection before calling it.
func (m *Manager) Fetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid fetch form", http.StatusBadRequest)
		return
	}
	target := r.PostForm.Get("path")
	modulePath, version, ok := moduleVersionFromPath(target)
	if !ok {
		http.Error(w, "a valid module path and explicit canonical version are required", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if m.upstream == nil {
		m.serveMissing(w, r, target, modulePath, version, "Fetching is disabled on this server. Ask an administrator to upload this version or enable upstream fetching.", http.StatusServiceUnavailable)
		return
	}
	if err := m.fetchMissing(r.Context(), modulePath, version); err != nil {
		message := "Unable to fetch this module version. Wait at least 30 seconds before retrying, or contact an administrator."
		status := http.StatusBadGateway
		if errors.Is(err, modproxy.ErrUpstreamNotFound) {
			message = "This version was not found in the configured upstream proxy. Check the module path and version before retrying."
			status = http.StatusNotFound
		}
		// Upstream errors can contain credentials or query parameters from proxy URLs.
		slog.WarnContext(r.Context(), "module fetch failed", "module", modulePath, "version", version, "status", status)
		m.serveMissing(w, r, target, modulePath, version, message, status)
		return
	}
	http.Redirect(w, r, (&url.URL{Path: target}).String(), http.StatusSeeOther)
}

var missingPage = template.Must(template.New("missing").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Module}}@{{.Version}} not available - gopkg</title>
  <link rel="stylesheet" href="/static/shared/color/color.css">
  <style>
    body { margin: 0; font: 16px/1.5 system-ui, sans-serif; color: var(--color-text, #202224); background: var(--color-background, #fff); }
    header { background: #007d9c; padding: 1rem; }
    header a { color: #fff; font-weight: 600; }
    main { max-width: 48rem; margin: 3rem auto; padding: 0 1rem; }
    h1 { font-size: 1.75rem; line-height: 1.25; }
    code { overflow-wrap: anywhere; }
    a { color: var(--color-text-link, #007d9c); text-underline-offset: .2em; }
    button { min-height: 44px; padding: .6rem 1.5rem; border: 1px solid #007d9c; border-radius: .25rem; background: #007d9c; color: #fff; font: inherit; font-weight: 600; cursor: pointer; }
    button:hover { background: #00657e; }
    :focus-visible { outline: 3px solid var(--color-text, #202224); outline-offset: 3px; }
    header a:focus-visible { outline-color: #fff; }
    .message { padding: 1rem; border: 1px solid var(--color-border, #c6c8ca); }
  </style>
</head>
<body>
  <header><nav aria-label="Main navigation"><a href="/">Go Packages</a></nav></header>
  <main>
    <h1>Module version not available</h1>
    <p><code>{{.Module}}@{{.Version}}</code> is not available in this server's local proxy index.</p>
    {{if .Message}}<p class="message" role="alert">{{.Message}}</p>{{end}}
    {{if .CanFetch}}
      <p>Fetch this version from the configured upstream proxy to make its documentation available locally. Nothing is downloaded until you select Fetch.</p>
      <form method="post" action="/-/fetch">
        <input type="hidden" name="path" value="{{.Path}}">
        <button type="submit">Fetch</button>
      </form>
      <p>The request may take a few minutes. If prompted, use your administrator credentials.</p>
    {{else}}
      <p>Upstream fetching is disabled. An administrator can <a href="/-/admin">upload this module version</a> or enable upstream fetching.</p>
    {{end}}
    <p><a href="/">Browse available packages</a></p>
  </main>
</body>
</html>`))

func (m *Manager) serveMissing(w http.ResponseWriter, r *http.Request, target, modulePath, version, message string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if err := missingPage.Execute(w, struct {
		Path, Module, Version, Message string
		CanFetch                       bool
	}{target, modulePath, version, message, m.upstream != nil}); err != nil {
		slog.ErrorContext(r.Context(), "render missing module page failed", "error", err)
	}
}

func (m *Manager) Stats() Stats {
	current := m.current.Load()
	if current == nil {
		return Stats{}
	}
	return current.stats
}

func (m *Manager) Reload(ctx context.Context) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reload(ctx)
}

func (m *Manager) AddVersion(ctx context.Context, modulePath, version string, info, mod, zip io.Reader) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.config.ProxyDirs) == 0 {
		return Stats{}, errors.New("no writable proxy directory is configured")
	}
	if err := modproxy.AddVersion(m.config.ProxyDirs[0], modulePath, version, info, mod, zip); err != nil {
		return Stats{}, err
	}
	return m.reload(ctx)
}

func (m *Manager) fetchMissing(ctx context.Context, modulePath, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.current.Load()
	if current != nil && current.store != nil && current.store.HasVersion(modulePath, version) {
		return nil
	}
	key := modulePath + "@" + version
	if failure, ok := m.fetchFailures[key]; ok && time.Now().Before(failure.expiresAt) {
		return failure.err
	}
	slog.InfoContext(ctx, "fetching missing module version", "module", modulePath, "version", version)
	fetched, err := m.upstream.Fetch(ctx, modulePath, version)
	if err != nil {
		m.fetchFailures[key] = fetchFailure{err: err, expiresAt: time.Now().Add(30 * time.Second)}
		return err
	}
	defer fetched.Zip.Close()
	err = modproxy.AddVersion(
		m.config.ProxyDirs[0],
		modulePath,
		version,
		bytes.NewReader(fetched.Info),
		bytes.NewReader(fetched.Mod),
		fetched.Zip,
	)
	if err != nil && !errors.Is(err, modproxy.ErrVersionExists) {
		m.fetchFailures[key] = fetchFailure{err: err, expiresAt: time.Now().Add(30 * time.Second)}
		return err
	}
	if _, err := m.reload(ctx); err != nil {
		m.fetchFailures[key] = fetchFailure{err: err, expiresAt: time.Now().Add(30 * time.Second)}
		return err
	}
	delete(m.fetchFailures, key)
	slog.InfoContext(ctx, "missing module version fetched", "module", modulePath, "version", version)
	return nil
}

func moduleVersionFromPath(requestPath string) (string, string, bool) {
	if !strings.HasPrefix(requestPath, "/") || strings.ContainsAny(requestPath, "?#\\") {
		return "", "", false
	}
	requestPath = strings.TrimPrefix(requestPath, "/")
	at := strings.IndexByte(requestPath, '@')
	if at <= 0 {
		return "", "", false
	}
	modulePath := requestPath[:at]
	version := requestPath[at+1:]
	suffix := ""
	if slash := strings.IndexByte(version, '/'); slash >= 0 {
		suffix = version[slash:]
		version = version[:slash]
	}
	if module.Check(modulePath, version) != nil || module.CanonicalVersion(version) != version ||
		module.CheckImportPath(modulePath+suffix) != nil {
		return "", "", false
	}
	return modulePath, version, true
}

func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 || len(m.config.ProxyDirs) == 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fingerprint, err := modproxy.Fingerprint(m.config.ProxyDirs)
			if err != nil {
				slog.ErrorContext(ctx, "proxy change check failed", "error", err)
				continue
			}
			current := m.current.Load()
			if current != nil && current.fingerprint == fingerprint {
				continue
			}
			stats, reloaded, err := m.reloadIfChanged(ctx, fingerprint)
			if err != nil {
				slog.ErrorContext(ctx, "proxy index reload failed", "error", err)
				continue
			}
			if reloaded {
				slog.InfoContext(ctx, "proxy index reloaded",
					"proxy_modules", stats.ProxyModules,
					"search_packages", stats.SearchPackages,
				)
			}
		}
	}
}

func (m *Manager) reloadIfChanged(ctx context.Context, fingerprint uint64) (Stats, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.current.Load()
	if current != nil && current.fingerprint == fingerprint {
		return current.stats, false, nil
	}
	stats, err := m.reload(ctx)
	return stats, err == nil, err
}

func (m *Manager) excludeModule(modulePath string) bool {
	for _, pattern := range m.config.Exclude {
		if matched, _ := doublestar.Match(pattern, modulePath); matched {
			return true
		}
	}
	return false
}

func (m *Manager) reload(ctx context.Context) (Stats, error) {
	fingerprint, err := modproxy.Fingerprint(m.config.ProxyDirs)
	if err != nil {
		return Stats{}, err
	}
	localModules, err := modproxy.DiscoverLocal(m.config.Paths)
	if err != nil {
		return Stats{}, err
	}
	var proxyHandler http.Handler
	var proxyModules []string
	var searchPackages []pkgsiteembed.SearchPackage
	var store *modproxy.Store
	if len(m.config.ProxyDirs) > 0 {
		store, err = modproxy.Open(m.config.ProxyDirs)
		if err != nil {
			return Stats{}, err
		}
		proxyHandler = store
		proxyModules = store.Modules()
		for _, pkg := range store.Packages() {
			searchPackages = append(searchPackages, pkgsiteembed.SearchPackage{
				Name:          pkg.Name,
				Path:          pkg.Path,
				ModulePath:    pkg.ModulePath,
				Version:       pkg.Version,
				Synopsis:      pkg.Synopsis,
				CommitTime:    pkg.CommitTime,
				Licenses:      pkg.Licenses,
				ImportedBy:    pkg.ImportedBy,
				NumImportedBy: pkg.NumImportedBy,
			})
		}
	}
	if len(localModules) == 0 && len(proxyModules) == 0 && m.upstream == nil {
		return Stats{}, errors.New("no Go modules found in the configured directories")
	}
	proxyModules = slices.DeleteFunc(proxyModules, m.excludeModule)
	searchPackages = slices.DeleteFunc(searchPackages, func(pkg pkgsiteembed.SearchPackage) bool {
		return m.excludeModule(pkg.ModulePath)
	})
	handler, err := pkgsiteembed.NewHandler(ctx, pkgsiteembed.Config{
		Paths:          localModules,
		ProxyHandler:   proxyHandler,
		ProxyModules:   proxyModules,
		SearchPackages: searchPackages,
		ListModules:    true,
		Offline:        true,
		ExcludeModule:  m.excludeModule,
	})
	if err != nil {
		return Stats{}, fmt.Errorf("build pkgsite server: %w", err)
	}
	stats := Stats{
		LocalModules:   len(localModules),
		ProxyModules:   len(proxyModules),
		SearchPackages: len(searchPackages),
		LoadedAt:       time.Now(),
	}
	m.current.Store(&snapshot{handler: handler, stats: stats, fingerprint: fingerprint, store: store})
	return stats, nil
}
