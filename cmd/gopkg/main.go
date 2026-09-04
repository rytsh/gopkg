package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
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
	flags := flag.NewFlagSet("gopkg", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	var dirs stringList
	var proxyDirs stringList
	addr := flags.String("http", ":8080", "HTTP listen address")
	adminToken := flags.String("admin-token", "", "basic-auth password for proxy mutations (empty disables administration)")
	fetchMissing := flags.Bool("fetch-missing", false, "fetch explicit missing module versions from GOPROXY")
	fetchTimeout := flags.Duration("fetch-timeout", 2*time.Minute, "total timeout for an upstream module fetch")
	refreshInterval := flags.Duration("refresh", 30*time.Second, "proxy directory change check interval (0 disables)")
	showVersion := flags.Bool("version", false, "print version information and exit")
	flags.Var(&dirs, "dir", "local directory containing one or more Go modules (repeatable)")
	flags.Var(&proxyDirs, "proxy-dir", "Athens disk storage or GOPROXY directory (repeatable)")
	upstreamProxy := flags.String("upstream-proxy", "", "upstream GOPROXY list (defaults to the GOPROXY environment variable)")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: gopkg [flags] [LOCAL_DIR ...]\n\n")
		fmt.Fprintln(flags.Output(), "Serves the official pkgsite interface from local modules and offline proxy storage.")
		fmt.Fprintln(flags.Output(), "A local directory is scanned recursively for go.mod files. Proxy directories")
		fmt.Fprintln(flags.Output(), "may use Athens disk layout or the standard GOPROXY @v layout.")
		fmt.Fprintln(flags.Output())
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Printf("gopkg version:%s commit:%s date:%s\n", version, commit, date)
		return nil
	}
	selectedUpstream := ""
	if *fetchMissing {
		selectedUpstream = strings.TrimSpace(*upstreamProxy)
		if selectedUpstream == "" {
			selectedUpstream = strings.TrimSpace(os.Getenv("GOPROXY"))
		}
		if selectedUpstream == "" {
			return errors.New("-fetch-missing requires -upstream-proxy or GOPROXY")
		}
	}
	if err := enforceOfflineGoEnvironment(); err != nil {
		return err
	}
	dirs = append(dirs, flags.Args()...)
	if len(dirs) == 0 && len(proxyDirs) == 0 {
		dirs = append(dirs, ".")
	}
	siteManager, err := site.New(ctx, site.Config{
		Paths:         dirs,
		ProxyDirs:     proxyDirs,
		UpstreamProxy: selectedUpstream,
		FetchTimeout:  *fetchTimeout,
	})
	if err != nil {
		return err
	}
	go siteManager.Watch(ctx, *refreshInterval)
	return appserver.Start(ctx, appserver.Config{
		Address:         *addr,
		FetchMissing:    *fetchMissing,
		AdminToken:      *adminToken,
		Version:         version,
		Commit:          commit,
		RefreshInterval: *refreshInterval,
	}, siteManager)
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
