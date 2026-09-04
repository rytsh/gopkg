package site

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rytsh/gopkg/internal/modproxy"
	"golang.org/x/mod/module"
	"golang.org/x/pkgsite/cmd/pkgsiteembed"
)

type Config struct {
	Paths         []string
	ProxyDirs     []string
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
	if r.Method == http.MethodGet {
		modulePath, version, ok := moduleVersionFromPath(r.URL.Path)
		if ok && (current.store == nil || !current.store.HasVersion(modulePath, version)) {
			if m.upstream == nil {
				http.NotFound(w, r)
				return
			}
			if err := m.fetchMissing(r.Context(), modulePath, version); err != nil {
				if errors.Is(err, modproxy.ErrUpstreamNotFound) {
					http.NotFound(w, r)
					return
				}
				slog.ErrorContext(r.Context(), "on-demand module fetch failed",
					"module", modulePath,
					"version", version,
					"error", err,
				)
				http.Error(w, "unable to fetch requested module version", http.StatusBadGateway)
				return
			}
			current = m.current.Load()
		}
	}
	current.handler.ServeHTTP(w, r)
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
	requestPath = strings.TrimPrefix(requestPath, "/")
	at := strings.IndexByte(requestPath, '@')
	if at <= 0 {
		return "", "", false
	}
	modulePath := requestPath[:at]
	version := requestPath[at+1:]
	if slash := strings.IndexByte(version, '/'); slash >= 0 {
		version = version[:slash]
	}
	if module.Check(modulePath, version) != nil {
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
	handler, err := pkgsiteembed.NewHandler(ctx, pkgsiteembed.Config{
		Paths:          localModules,
		ProxyHandler:   proxyHandler,
		ProxyModules:   proxyModules,
		SearchPackages: searchPackages,
		ListModules:    true,
		Offline:        true,
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
