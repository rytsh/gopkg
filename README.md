# gopkg

`gopkg` serves the official [pkgsite](https://github.com/golang/pkgsite) web
interface for private and offline Go code. It accepts local source trees,
[Athens](https://docs.gomods.io/) disk storage, and standard
[`GOPROXY`](https://go.dev/ref/mod#goproxy-protocol) directories without
publishing source code externally.

See [DETAILS.md](DETAILS.md) for usage examples, storage layouts, administration,
offline behavior, command-line options, and development notes.

## Installation

### Latest release on Linux (x86-64)

```sh
mkdir -p ~/bin
curl -fSL https://github.com/rytsh/gopkg/releases/latest/download/gopkg_Linux_x86_64.tar.gz | tar -xz --overwrite -C ~/bin/ gopkg
```

Add `~/bin` to `PATH` if needed. Archives for Linux, macOS, Windows, x86-64,
and ARM64 are available on the
[releases page](https://github.com/rytsh/gopkg/releases/latest).

### Build from source

```sh
git clone https://github.com/rytsh/gopkg.git
cd gopkg
go build -o gopkg ./cmd/gopkg
```

## Docker

Release tags publish `linux/amd64` and `linux/arm64` images to GitHub Container
Registry:

```sh
docker run --rm -p 8080:8080 \
  -e GOPKG_ADMIN_TOKEN='replace-me' \
  -e GOPKG_DIR=/src \
  -v "$PWD:/src:ro" \
  -v gopkg-proxy:/proxy \
  ghcr.io/rytsh/gopkg:latest
```

The image runs as UID/GID `65532`. `GOPKG_DIR` is optional and is scanned
recursively for local Go modules. The `/proxy` volume stores standard GOPROXY or
Athens data and must be writable by UID `65532` when uploads are enabled.

Open <http://localhost:8080> after the container starts.

## Configuration

`gopkg` loads configuration with
[`github.com/rakunlabs/chu`](https://github.com/rakunlabs/chu) in this order:
defaults, `gopkg.toml`/`gopkg.yaml`/`gopkg.yml`/`gopkg.json`, an optional HTTP
source, and environment variables. Set `CONFIG_FILE_GOPKG` (or the shared
`CONFIG_FILE`) to select another file.

Environment variables use the `GOPKG_` prefix. List values such as `GOPKG_DIR`
and `GOPKG_PROXY_DIR` are comma-separated. Command-line flags override loaded
values.

```yaml
# HTTP address the documentation server listens on.
http: ":8080"
# Local directories scanned recursively for Go modules.
dir:
  - /srv/projects
# Athens storage or standard GOPROXY directories to serve.
proxy_dir:
  - /var/lib/goproxy
# Hide matching Go module paths from the homepage and search results.
exclude:
  - "balbbla.com/**"
  - "github.com/acme/legacy"
# Password for admin, upload, and fetch endpoints; empty accepts requests without authentication.
admin_token: ""
# Enable the Fetch button for missing versions; visiting a page never downloads them.
fetch_missing: false
# Maximum duration allowed for one upstream module fetch.
fetch_timeout: 2m
# Interval for detecting proxy directory changes; use 0 to disable polling.
refresh: 10m
# Upstream GOPROXY URL or fallback list; empty uses the GOPROXY environment variable.
upstream_proxy: ""
```

`exclude` uses [doublestar](https://github.com/bmatcuk/doublestar) patterns:
`*` matches within one path segment, while `**` matches across segments.
Patterns match case-sensitive Go module paths, without `https://` or `@version`.
A matching module and all its packages are hidden from the homepage and search,
for both local source trees and proxy storage. This is a visibility filter, not
access control: direct documentation URLs and stored files remain available.
Set `GOPKG_EXCLUDE='balbbla.com/**,github.com/acme/legacy'` to override the list
through the environment. Invalid patterns fail startup. Restart the server after
changing the configuration; index reloads retain the startup patterns.

The Docker image supports `GOPKG_HTTP`, `GOPKG_DIR`, `GOPKG_PROXY_DIR`, `GOPKG_EXCLUDE`,
`GOPKG_ADMIN_TOKEN`, `GOPKG_FETCH_MISSING`, `GOPKG_FETCH_TIMEOUT`,
`GOPKG_REFRESH`, and `GOPROXY`. Its Turna configuration writes the resolved
settings to `/etc/gopkg.yaml` before starting `gopkg`. A customized Turna
configuration can be mounted at `/etc/turna/turna.yaml`.
