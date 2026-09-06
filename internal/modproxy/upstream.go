package modproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/mod/module"
)

var ErrUpstreamNotFound = errors.New("module version not found in upstream proxy")

type FetchedVersion struct {
	Info []byte
	Mod  []byte
	Zip  io.ReadCloser
}

type upstreamEntry struct {
	base        *url.URL
	directive   string
	fallbackAny bool
}

type Upstream struct {
	client  *http.Client
	entries []upstreamEntry
}

func NewUpstream(proxyList string, timeout time.Duration) (*Upstream, error) {
	if timeout <= 0 {
		return nil, errors.New("upstream timeout must be positive")
	}
	entries, err := parseUpstreamEntries(proxyList)
	if err != nil {
		return nil, err
	}
	hasHTTP := false
	for _, entry := range entries {
		hasHTTP = hasHTTP || entry.base != nil
	}
	if !hasHTTP {
		return nil, errors.New("GOPROXY must contain at least one HTTP(S) URL")
	}
	return &Upstream{
		client:  &http.Client{Timeout: timeout},
		entries: entries,
	}, nil
}

func (u *Upstream) Fetch(ctx context.Context, modulePath, version string) (*FetchedVersion, error) {
	return u.fetch(ctx, modulePath, version, false)
}

// Warm consumes artifact GETs to populate the upstream's backing storage without
// retaining artifacts locally. GOPROXY has no cache-only or no-body operation.
func (u *Upstream) Warm(ctx context.Context, modulePath, version string) error {
	_, err := u.fetch(ctx, modulePath, version, true)
	return err
}

func (u *Upstream) fetch(ctx context.Context, modulePath, version string, discard bool) (*FetchedVersion, error) {
	if err := module.Check(modulePath, version); err != nil {
		return nil, fmt.Errorf("invalid module version %s@%s: %w", modulePath, version, err)
	}
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return nil, fmt.Errorf("escape module path: %w", err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		return nil, fmt.Errorf("escape module version: %w", err)
	}

	var lastErr error
	for index, entry := range u.entries {
		if entry.directive != "" {
			lastErr = fmt.Errorf("GOPROXY directive %q is unavailable for on-demand fetch", entry.directive)
		} else {
			fetched, err := u.fetchFrom(ctx, entry.base, escapedPath, escapedVersion, discard)
			if err == nil {
				return fetched, nil
			}
			lastErr = err
		}
		if index == len(u.entries)-1 || (!entry.fallbackAny && !errors.Is(lastErr, ErrUpstreamNotFound)) {
			break
		}
	}
	return nil, fmt.Errorf("fetch %s@%s from GOPROXY: %w", modulePath, version, lastErr)
}

func (u *Upstream) fetchFrom(ctx context.Context, base *url.URL, escapedPath, escapedVersion string, discard bool) (*FetchedVersion, error) {
	prefix := escapedPath + "/@v/" + escapedVersion
	if discard {
		for _, artifact := range []struct {
			ext   string
			limit int64
		}{{".info", 1 << 20}, {".mod", 16 << 20}, {".zip", 500 << 20}} {
			response, err := u.request(ctx, base, prefix+artifact.ext)
			if err != nil {
				return nil, err
			}
			n, err := io.Copy(io.Discard, io.LimitReader(response.Body, artifact.limit+1))
			response.Body.Close()
			if err != nil {
				return nil, err
			}
			if n > artifact.limit {
				return nil, fmt.Errorf("upstream %s artifact exceeds %d bytes", artifact.ext, artifact.limit)
			}
		}
		return nil, nil
	}
	info, err := u.readArtifact(ctx, base, prefix+".info", 1<<20)
	if err != nil {
		return nil, err
	}
	mod, err := u.readArtifact(ctx, base, prefix+".mod", 16<<20)
	if err != nil {
		return nil, err
	}
	zipResponse, err := u.request(ctx, base, prefix+".zip")
	if err != nil {
		return nil, err
	}
	if zipResponse.ContentLength > 500<<20 {
		zipResponse.Body.Close()
		return nil, errors.New("upstream module zip exceeds 500 MiB")
	}
	return &FetchedVersion{Info: info, Mod: mod, Zip: zipResponse.Body}, nil
}

func (u *Upstream) readArtifact(ctx context.Context, base *url.URL, artifact string, limit int64) ([]byte, error) {
	response, err := u.request(ctx, base, artifact)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return readLimited(response.Body, limit)
}

func (u *Upstream) request(ctx context.Context, base *url.URL, artifact string) (*http.Response, error) {
	endpoint := *base
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/" + artifact
	endpoint.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "gopkg")
	response, err := u.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	defer response.Body.Close()
	_, _ = io.CopyN(io.Discard, response.Body, 4<<10)
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return nil, fmt.Errorf("%w: %s", ErrUpstreamNotFound, response.Status)
	}
	return nil, fmt.Errorf("upstream proxy returned %s", response.Status)
}

func parseUpstreamEntries(proxyList string) ([]upstreamEntry, error) {
	proxyList = strings.TrimSpace(proxyList)
	if proxyList == "" {
		return nil, errors.New("GOPROXY is empty")
	}
	var entries []upstreamEntry
	start := 0
	for index := 0; index <= len(proxyList); index++ {
		if index < len(proxyList) && proxyList[index] != ',' && proxyList[index] != '|' {
			continue
		}
		value := strings.TrimSpace(proxyList[start:index])
		if value == "" {
			return nil, errors.New("GOPROXY contains an empty entry")
		}
		entry := upstreamEntry{fallbackAny: index < len(proxyList) && proxyList[index] == '|'}
		switch value {
		case "direct", "off":
			entry.directive = value
		default:
			parsed, err := url.Parse(value)
			if err != nil {
				return nil, fmt.Errorf("parse GOPROXY entry %q: %w", value, err)
			}
			if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return nil, fmt.Errorf("GOPROXY entry %q must be an HTTP(S) URL", value)
			}
			parsed.Path = strings.TrimSuffix(parsed.Path, "/")
			entry.base = parsed
		}
		entries = append(entries, entry)
		start = index + 1
	}
	return entries, nil
}
