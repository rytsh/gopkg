# gopkg

[![License](https://img.shields.io/github/license/rytsh/gopkg?style=flat-square)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/rytsh/gopkg?style=flat-square)](https://github.com/rytsh/gopkg/blob/main/go.mod)
[![Release](https://img.shields.io/github/v/release/rytsh/gopkg?style=flat-square)](https://github.com/rytsh/gopkg/releases/latest)

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

<details>
<summary>Adding modules remotely with curl</summary>

You can add a Go module version to the server's proxy storage over HTTP, either
by requesting an upstream download or by uploading module files directly.
Replace `https://gopkg.example.com` with your server URL and set the client-side
`GOPKG_ADMIN_TOKEN` variable to the server's configured admin token.
Use HTTPS and a non-empty `admin_token` for remote access; an empty token allows
requests without authentication.

### Fetch from an upstream proxy

Enable fetching in the server configuration and provide a writable proxy directory:

```yaml
proxy_dir:
  - /srv/goproxy
admin_token: "replace-with-a-strong-token"
fetch_missing: true
upstream_proxy: "https://proxy.golang.org"
```

Send the module path and explicit version as a URL-encoded form field:

```sh
curl --fail-with-body -i \
  -H "Authorization: Bearer $GOPKG_ADMIN_TOKEN" \
  --data-urlencode 'path=/github.com/worldline-go/wkafka@v0.6.7' \
  'https://gopkg.example.com/-/fetch'
```

The server downloads the module, updates the index, and responds with
`303 See Other`; the `Location` header points to its documentation.
`latest` and direct VCS downloads are not supported. Fetching requires upstream
network access and returns `503` when `fetch_missing` is disabled.

### Upload module files

If you already have the module files, upload them as multipart form data
instead of JSON. This method does not require upstream access or `fetch_missing`:

```sh
curl --fail-with-body -i \
  -H "Authorization: Bearer $GOPKG_ADMIN_TOKEN" \
  -H 'Accept: application/json' \
  -F 'module=example.com/project' \
  -F 'version=v1.2.3' \
  -F 'info=@./v1.2.3.info' \
  -F 'mod=@./v1.2.3.mod' \
  -F 'zip=@./v1.2.3.zip' \
  'https://gopkg.example.com/-/modules'
```

The files are read from the machine running `curl`. All three files are required
and must match the module path and version. The ZIP must use the standard Go
module ZIP format, not an arbitrary source archive.

A successful upload returns `201 Created` with JSON and automatically refreshes
the index. Both methods write to the first configured `proxy_dir`, which must be
writable by the server. Uploads do not overwrite existing versions.

See [DETAILS.md](DETAILS.md#refreshing-and-adding-versions) for more administration options.

</details>
