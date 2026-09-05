# gopkg details

The pkgsite source is vendored at `third_party/pkgsite` and pinned to `v0.4.0`.
`gopkg` adds recursive local-module discovery and an in-process GOPROXY adapter
for filesystem caches.

For installation, Docker, and configuration instructions, see
[README.md](README.md).

## Features

- Official pkgsite templates, assets, routing, and package documentation rendering
- Recursive local module discovery through `go.mod` files
- Athens disk storage and standard `GOPROXY` directory support
- Multiple local and proxy sources in one server
- Proxy modules listed on the pkgsite homepage
- Module archives fetched lazily through an in-process GOPROXY adapter
- No outbound network requests by default; Go module, checksum, VCS, source-link, and toolchain downloads are disabled
- Optional explicit-version fetch from a configured upstream GOPROXY
- Automatic proxy-directory change detection with atomic index replacement
- Browser and HTTP upload of complete standard GOPROXY module versions
- Graceful lifecycle, structured logs, and HTTP middleware through Rakunlabs `into`, `logi`, and Ada v0.5.1

## Quick start

Run inside a Go module:

```sh
gopkg
```

Then open <http://localhost:8080>. The default `:8080` address listens on all
interfaces so the site remains reachable from a container or WSL host. Use
`-http 127.0.0.1:8080` to restrict access to the local machine.

When neither `-dir` nor `-proxy-dir` is provided, `gopkg` scans the current
directory.

## Local modules

A local directory is scanned recursively for `go.mod` files. This allows one
server to document a module, a multi-module repository, or a directory
containing several repositories.

```sh
gopkg -dir ~/projects
```

Flags may be repeated:

```sh
gopkg \
  -dir ~/projects/backend \
  -dir ~/projects/libraries
```

Positional paths are equivalent to `-dir`:

```sh
gopkg ~/projects/backend ~/projects/libraries
```

Nested module boundaries are respected. Hidden directories, `vendor`,
`testdata`, and `node_modules` are skipped.

## Athens disk storage

Point `-proxy-dir` at the value used by Athens as `ATHENS_DISK_STORAGE_ROOT`:

```sh
gopkg -proxy-dir /var/lib/athens
```

The supported Athens layout is:

```text
<root>/<module-path>/<version>/go.mod
<root>/<module-path>/<version>/source.zip
```

## Standard GOPROXY directory

A file-based Go module proxy can be served directly:

```sh
gopkg -proxy-dir /srv/goproxy
```

The standard archive layout is:

```text
<root>/<escaped-module-path>/@v/<version>.zip
```

Multiple local and proxy sources can be combined:

```sh
gopkg \
  -dir ~/projects \
  -proxy-dir /var/lib/athens \
  -proxy-dir /srv/goproxy \
  -http 0.0.0.0:8080
```

Local modules are served with pkgsite's local version semantics. For proxy
storage, search indexes package paths, declared package names, and package
synopses from each module's latest archive. Latest-version selection follows
GOPROXY rules: stable releases take precedence over prereleases, and
pseudo-versions are selected by publication time when no tagged version exists.

Search results also show publication time, detected licenses, and reverse
dependencies. Imported-by counts and lists describe the latest packages present
in the configured local proxy directories; they are not global pkg.go.dev
counts. When two proxy directories contain the same module and version, the
first configured source wins.

### Refreshing and adding versions

`gopkg` fingerprints relevant `.zip`, `.mod`, `.info`, `go.mod`, and `list`
artifacts under each proxy directory. Changes are checked every 30 seconds by
default. A changed directory is rebuilt in the background; active requests keep
using the previous immutable index until the replacement is ready. Configure
the interval with `-refresh`, or disable polling with `-refresh 0`.

The `/-/admin` page, immediate refresh, and module upload endpoints are always
enabled. Set `-admin-token` to require authentication; the page uses HTTP Basic
authentication, where the username is ignored and the password is the
configured token. Without a token, all requests are accepted without
authentication. An upload requires the version's
`.info`, `.mod`, and `.zip` files and writes standard GOPROXY layout into the
first `-proxy-dir`. Module paths, semantic versions, metadata, `go.mod`, and zip
prefixes are validated before publication. The same operations are available
over HTTP:

```sh
curl -u "gopkg:$GOPKG_ADMIN_TOKEN" -X POST http://localhost:8080/-/reload

curl -u "gopkg:$GOPKG_ADMIN_TOKEN" -X POST http://localhost:8080/-/modules \
  -F module=example.com/project \
  -F version=v1.2.3 \
  -F info=@v1.2.3.info \
  -F mod=@v1.2.3.mod \
  -F zip=@v1.2.3.zip
```

Uploads never fetch a module from the internet. Obtain the three proxy
artifacts outside the isolated runtime, then upload or copy them into the
mounted proxy directory.

### On-demand upstream fetch

On-demand fetch is opt-in. Set `-fetch-missing` and provide the upstream proxy
through `-upstream-proxy` or the `GOPROXY` environment variable:

```sh
GOPROXY=https://proxy.golang.org \
  gopkg -proxy-dir /srv/goproxy -fetch-missing
```

An explicit version URL then fetches a missing module and serves the same
request:

```text
http://localhost:8080/github.com/worldline-go/wkafka@v0.6.7
```

`gopkg` requests the version's `.info`, `.mod`, and `.zip` artifacts from the
configured GOPROXY, validates them, publishes them atomically into the first
`-proxy-dir`, rebuilds the index, and renders the package page. Subsequent
requests use the local files and do not contact the upstream proxy.

Only syntactically valid, explicit module versions are fetched. `latest` and
direct VCS downloads are not supported. Comma and pipe fallback behavior
follows the GOPROXY protocol. Fetches have a configurable timeout, a 500 MiB zip
limit, and a 30-second negative-result cache.

Enabling this option lets any HTTP client request valid module versions and
therefore consume upstream bandwidth and local disk. Use it only on a trusted
network or behind an authenticated reverse proxy. `-admin-token` protects the
administration endpoints; it does not protect URL-triggered fetches.

### Offline runtime

At startup, `gopkg` still enforces `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off`, and `GOVCS=*:off` for Go subprocesses. Proxy reads use an
in-process HTTP transport; repository, deps.dev, CodeWiki, remote source
metadata, remote standard-library lookups, and direct VCS access remain
disabled. Without `-fetch-missing`, runtime makes no outbound connections. When
enabled, only the controlled HTTP(S) GOPROXY client performs outbound requests.

Standard-library documentation is loaded only from the local `GOROOT`. A host
installation therefore needs a compatible local Go distribution. The published
container image includes one. If no usable `GOROOT` exists, proxy and local
module documentation remains available, but standard-library documentation
does not.

## Command-line options

```text
Usage: gopkg [flags] [LOCAL_DIR ...]

  -admin-token string
        basic-auth password for proxy mutations (empty allows unauthenticated access)
  -dir value
        local directory containing one or more Go modules (repeatable)
  -fetch-missing
        fetch explicit missing module versions from GOPROXY
  -fetch-timeout duration
        total timeout for an upstream module fetch (default 2m0s)
  -http string
        HTTP listen address (default ":8080")
  -proxy-dir value
        Athens disk storage or GOPROXY directory (repeatable)
  -refresh duration
        proxy directory change check interval (0 disables) (default 30s)
  -upstream-proxy string
        upstream GOPROXY list (defaults to the GOPROXY environment variable)
  -version
        print version information and exit
```

The health endpoint is available at `/-/healthz`, runtime statistics at
`/-/status`, and proxy administration at `/-/admin`.

## Development

Running `make` without a target prints the available commands:

```sh
make
make build
make test
make run
make clean
```

## Releasing

Tags matching `v*` trigger the GitHub Actions release workflow. GoReleaser
builds Linux, macOS, and Windows archives for `amd64` and `arm64`, publishes
SHA-256 checksums, and pushes a multi-platform Alpine image to
`ghcr.io/rytsh/gopkg` with the release and `latest` tags.

```sh
git tag v0.1.0
git push origin v0.1.0
```

To validate or build the release locally with
[GoReleaser](https://goreleaser.com/):

```sh
goreleaser check
goreleaser release --snapshot --clean
```

## License

The `gopkg` integration is licensed under [MIT](LICENSE). Vendored pkgsite code
retains the upstream BSD-3-Clause license at
[`third_party/pkgsite/LICENSE`](third_party/pkgsite/LICENSE).
