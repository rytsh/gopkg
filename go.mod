module github.com/rytsh/gopkg

go 1.27

require (
	github.com/bmatcuk/doublestar/v4 v4.9.1
	github.com/rakunlabs/ada v0.5.1
	github.com/rakunlabs/ada/middleware/log v0.5.1
	github.com/rakunlabs/ada/middleware/recover v0.5.1
	github.com/rakunlabs/ada/middleware/requestid v0.5.1
	github.com/rakunlabs/ada/middleware/server v0.5.1
	github.com/rakunlabs/chu v0.5.0
	github.com/rakunlabs/into v0.5.3
	github.com/rakunlabs/logi v0.4.6
	golang.org/x/mod v0.40.0
	golang.org/x/pkgsite v0.4.0
)

require (
	github.com/BurntSushi/toml v1.5.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/goccy/go-yaml v1.18.0 // indirect
	github.com/google/licensecheck v0.3.1 // indirect
	github.com/google/safehtml v0.0.3-0.20211026203422-d6f0e11a5516 // indirect
	github.com/hashicorp/go-envparse v0.1.0 // indirect
	github.com/lmittmann/tint v1.1.2 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	github.com/rakunlabs/gofret v0.2.1 // indirect
	github.com/rakunlabs/ok v0.1.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/twmb/tlscfg v1.3.0 // indirect
	github.com/xhit/go-str2duration/v2 v2.1.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	rsc.io/markdown v0.0.0-20231214224604-88bb533a6020 // indirect
)

replace golang.org/x/pkgsite => ./third_party/pkgsite
