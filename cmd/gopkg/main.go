package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"github.com/rytsh/gopkg/internal/config"
	appserver "github.com/rytsh/gopkg/internal/server"
	"github.com/rytsh/gopkg/internal/site"
)

var (
	version = "v0.0.0"
	commit  = "-"
	date    = "-"
)

type stringList []string

func (v *stringList) String() string { return strings.Join(*v, ",") }

func (v *stringList) Set(value string) error {
	if value == "" {
		return errors.New("path cannot be empty")
	}
	*v = append(*v, value)
	return nil
}

func main() {
	logger := logi.InitializeLog(logi.WithCaller(false))
	into.Init(func(ctx context.Context) error {
		err := run(ctx, os.Args[1:])
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	},
		into.WithLogger(logger),
		into.WithMsgf("gopkg version:[%s] commit:[%s] date:[%s]", version, commit, date),
	)
}

func run(ctx context.Context, args []string) error {
	cfg, showVersion, err := loadConfig(ctx, args)
	if err != nil {
		return err
	}
	if showVersion {
		fmt.Printf("gopkg version:%s commit:%s date:%s\n", version, commit, date)
		return nil
	}

	selectedUpstream := ""
	if cfg.FetchMissing {
		selectedUpstream = strings.TrimSpace(cfg.UpstreamProxy)
		if selectedUpstream == "" {
			selectedUpstream = strings.TrimSpace(os.Getenv("GOPROXY"))
		}
		if selectedUpstream == "" {
			return errors.New("fetch_missing requires upstream_proxy or GOPROXY")
		}
	}
	if err := enforceOfflineGoEnvironment(); err != nil {
		return err
	}
	if len(cfg.Dirs) == 0 && len(cfg.ProxyDirs) == 0 {
		cfg.Dirs = append(cfg.Dirs, ".")
	}
	siteManager, err := site.New(ctx, site.Config{
		Paths:         cfg.Dirs,
		ProxyDirs:     cfg.ProxyDirs,
		Exclude:       cfg.Exclude,
		UpstreamProxy: selectedUpstream,
		FetchTimeout:  cfg.FetchTimeout,
	})
	if err != nil {
		return err
	}
	go siteManager.Watch(ctx, cfg.RefreshInterval)
	return appserver.Start(ctx, appserver.Config{
		Address:         cfg.HTTP,
		FetchMissing:    cfg.FetchMissing,
		AdminToken:      cfg.AdminToken,
		Version:         version,
		Commit:          commit,
		RefreshInterval: cfg.RefreshInterval,
	}, siteManager)
}

func loadConfig(ctx context.Context, args []string) (*config.Config, bool, error) {
	cfg, err := config.Load(ctx, version)
	if err != nil {
		return nil, false, fmt.Errorf("load configuration: %w", err)
	}

	flags := flag.NewFlagSet("gopkg", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	var dirs stringList
	var proxyDirs stringList
	addr := flags.String("http", cfg.HTTP, "HTTP listen address")
	adminToken := flags.String("admin-token", "", "basic-auth password for proxy mutations (empty allows unauthenticated access)")
	fetchMissing := flags.Bool("fetch-missing", cfg.FetchMissing, "allow explicit missing module versions to be fetched from GOPROXY using the Fetch button")
	fetchTimeout := flags.Duration("fetch-timeout", cfg.FetchTimeout, "total timeout for an upstream module fetch")
	refreshInterval := flags.Duration("refresh", cfg.RefreshInterval, "proxy directory change check interval (0 disables)")
	showVersion := flags.Bool("version", false, "print version information and exit")
	flags.Var(&dirs, "dir", "local directory containing one or more Go modules (repeatable)")
	flags.Var(&proxyDirs, "proxy-dir", "Athens disk storage or GOPROXY directory (repeatable)")
	upstreamProxy := flags.String("upstream-proxy", cfg.UpstreamProxy, "upstream GOPROXY list (defaults to the GOPROXY environment variable)")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: gopkg [flags] [LOCAL_DIR ...]\n\n")
		fmt.Fprintln(flags.Output(), "Serves the official pkgsite interface from local modules and offline proxy storage.")
		fmt.Fprintln(flags.Output(), "A local directory is scanned recursively for go.mod files. Proxy directories")
		fmt.Fprintln(flags.Output(), "may use Athens disk layout or the standard GOPROXY @v layout.")
		fmt.Fprintln(flags.Output())
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, false, err
	}

	adminTokenSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "admin-token" {
			adminTokenSet = true
		}
	})

	cfg.HTTP = *addr
	if adminTokenSet {
		cfg.AdminToken = *adminToken
	}
	cfg.FetchMissing = *fetchMissing
	cfg.FetchTimeout = *fetchTimeout
	cfg.RefreshInterval = *refreshInterval
	cfg.UpstreamProxy = *upstreamProxy
	if len(dirs) > 0 || len(flags.Args()) > 0 {
		cfg.Dirs = append(dirs, flags.Args()...)
	}
	if len(proxyDirs) > 0 {
		cfg.ProxyDirs = proxyDirs
	}

	return cfg, *showVersion, nil
}

func enforceOfflineGoEnvironment() error {
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "GOTOOLCHAIN", value: "local"},
		{name: "GOPROXY", value: "off"},
		{name: "GOSUMDB", value: "off"},
		{name: "GOVCS", value: "*:off"},
	} {
		if err := os.Setenv(setting.name, setting.value); err != nil {
			return fmt.Errorf("set %s: %w", setting.name, err)
		}
	}
	return nil
}
