package pkgsiteembed

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	cmdpkgsite "golang.org/x/pkgsite/cmd/internal/pkgsite"
	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/fetch"
	"golang.org/x/pkgsite/internal/frontend"
	"golang.org/x/pkgsite/internal/fuzzy"
	"golang.org/x/pkgsite/internal/licenses"
	"golang.org/x/pkgsite/internal/middleware/timeout"
	"golang.org/x/pkgsite/internal/proxy"
	"golang.org/x/pkgsite/internal/source"
)

type Config struct {
	Paths          []string
	ProxyURL       string
	ProxyHandler   http.Handler
	ProxyModules   []string
	SearchPackages []SearchPackage
	UseCache       bool
	CacheDir       string
	ListModules    bool
	Offline        bool
}

type SearchPackage struct {
	Name          string
	Path          string
	ModulePath    string
	Version       string
	Synopsis      string
	CommitTime    time.Time
	Licenses      []string
	ImportedBy    []string
	NumImportedBy uint64
}

func DetectPackageLicenses(modulePath, version string, archive *zip.Reader, packageDirs []string) map[string][]string {
	detector := licenses.NewDetector(modulePath, version, archive, nil)
	result := make(map[string][]string, len(packageDirs))
	for _, dir := range packageDirs {
		_, detected := detector.PackageInfo(dir)
		seen := make(map[string]bool)
		for _, license := range detected {
			for _, licenseType := range license.Types {
				seen[licenseType] = true
			}
		}
		types := make([]string, 0, len(seen))
		for licenseType := range seen {
			types = append(types, licenseType)
		}
		sort.Strings(types)
		result[dir] = types
	}
	return result
}

func NewHandler(ctx context.Context, cfg Config) (http.Handler, error) {
	if cfg.ProxyURL != "" && cfg.ProxyHandler != nil {
		return nil, errors.New("ProxyURL and ProxyHandler are mutually exclusive")
	}
	if cfg.Offline && cfg.ProxyURL != "" {
		return nil, errors.New("ProxyURL is unavailable in offline mode")
	}
	serverConfig := cmdpkgsite.ServerConfig{
		Paths:                cfg.Paths,
		UseCache:             cfg.UseCache,
		CacheDir:             cfg.CacheDir,
		UseListedMods:        cfg.ListModules,
		UseLocalStdlib:       true,
		DisableRemoteStdlib:  cfg.Offline,
		DisableExternalLinks: cfg.Offline,
	}
	for _, modulePath := range cfg.ProxyModules {
		serverConfig.AdditionalModules = append(serverConfig.AdditionalModules, frontend.LocalModule{
			ModulePath: modulePath,
			Dir:        "proxy storage",
		})
	}

	if cfg.ProxyURL != "" || cfg.ProxyHandler != nil {
		proxyURL := cfg.ProxyURL
		var transport http.RoundTripper
		if cfg.ProxyHandler != nil {
			proxyURL = "http://gopkg.local"
			transport = handlerTransport{handler: cfg.ProxyHandler}
		}
		client, err := proxy.New(proxyURL, transport)
		if err != nil {
			return nil, fmt.Errorf("create module proxy client: %w", err)
		}
		disabledClient := client.WithFetchDisabled()
		serverConfig.Proxy = disabledClient
		var sourceClient *source.Client
		if !cfg.Offline {
			sourceClient = source.NewClient(&http.Client{Timeout: time.Second})
		}
		var moduleGetter fetch.ModuleGetter = fetch.NewProxyModuleGetter(disabledClient, sourceClient)
		if cfg.Offline {
			moduleGetter = &offlineModuleGetter{ModuleGetter: moduleGetter}
		}
		if len(cfg.SearchPackages) > 0 {
			moduleGetter = &searchableProxyGetter{
				ModuleGetter: moduleGetter,
				packages:     cfg.SearchPackages,
			}
		}
		serverConfig.ProxyGetter = moduleGetter
	}

	server, err := cmdpkgsite.BuildServer(ctx, serverConfig)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	server.Install(mux.Handle, nil, nil)
	return timeout.Timeout(54 * time.Second)(mux), nil
}

type handlerTransport struct {
	handler http.Handler
}

func (t handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

type offlineModuleGetter struct {
	fetch.ModuleGetter
}

func (g *offlineModuleGetter) SourceInfo(ctx context.Context, modulePath, version string) (*source.Info, error) {
	return source.ModuleInfo(ctx, source.NewStaticClient(), modulePath, version)
}

type searchableProxyGetter struct {
	fetch.ModuleGetter
	packages []SearchPackage
}

func (g *searchableProxyGetter) ImportedBy(packagePath string) []string {
	for _, pkg := range g.packages {
		if pkg.Path == packagePath {
			return append([]string(nil), pkg.ImportedBy...)
		}
	}
	return nil
}

func (g *searchableProxyGetter) Search(ctx context.Context, query string, limit int) ([]*internal.SearchResult, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || limit <= 0 {
		return nil, nil
	}
	matcher := fuzzy.NewSymbolMatcher(query)
	terms := strings.Fields(query)
	var results []*internal.SearchResult
	for i, pkg := range g.packages {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if strings.Contains("/"+pkg.Path+"/", "/internal/") {
			continue
		}
		packagePath := strings.ToLower(pkg.Path)
		name := strings.ToLower(pkg.Name)
		synopsis := strings.ToLower(pkg.Synopsis)
		score := searchScore(matcher, terms, query, packagePath, name, synopsis, pkg.Path == pkg.ModulePath)
		if score == 0 {
			continue
		}
		results = append(results, &internal.SearchResult{
			Name:          pkg.Name,
			PackagePath:   pkg.Path,
			ModulePath:    pkg.ModulePath,
			Version:       pkg.Version,
			Synopsis:      pkg.Synopsis,
			CommitTime:    pkg.CommitTime,
			Licenses:      pkg.Licenses,
			NumImportedBy: pkg.NumImportedBy,
			Score:         score,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].PackagePath < results[j].PackagePath
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	for i, result := range results {
		result.Offset = i
	}
	return results, nil
}

func searchScore(matcher *fuzzy.SymbolMatcher, terms []string, query, packagePath, name, synopsis string, moduleRoot bool) float64 {
	switch {
	case packagePath == query:
		return 10
	case name == query && moduleRoot:
		return 9
	case name == query:
		return 8
	case strings.HasSuffix(packagePath, "/"+query):
		return 7
	case strings.Contains(packagePath, query):
		return 6
	case strings.Contains(synopsis, query):
		return 5
	}
	for _, term := range terms {
		if !strings.Contains(packagePath, term) && !strings.Contains(name, term) && !strings.Contains(synopsis, term) {
			return fuzzyScore(matcher, packagePath)
		}
	}
	if len(terms) > 1 {
		return 5
	}
	return fuzzyScore(matcher, packagePath)
}

func fuzzyScore(matcher *fuzzy.SymbolMatcher, packagePath string) float64 {
	index, score := matcher.Match([]string{packagePath})
	if index < 0 {
		return 0
	}
	return score
}
