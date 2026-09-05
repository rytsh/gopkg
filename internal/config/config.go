package config

import (
	"context"
	"log/slog"
	"time"

	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/chu/loader/loaderenv"
)

const ServiceName = "gopkg"

type Config struct {
	HTTP            string        `cfg:"http" default:":8080"`
	Dirs            []string      `cfg:"dir"`
	ProxyDirs       []string      `cfg:"proxy_dir"`
	Exclude         []string      `cfg:"exclude"`
	AdminToken      string        `cfg:"admin_token" log:"-"`
	FetchMissing    bool          `cfg:"fetch_missing" default:"false"`
	FetchTimeout    time.Duration `cfg:"fetch_timeout" default:"2m"`
	RefreshInterval time.Duration `cfg:"refresh" default:"10m"`
	UpstreamProxy   string        `cfg:"upstream_proxy" log:"-"`
}

func Load(ctx context.Context, version string) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, ServiceName, &cfg,
		chu.WithLoaderOption(loaderenv.New(
			loaderenv.WithPrefix("GOPKG_"),
		)),
		chu.WithVersion(version),
	); err != nil {
		return nil, err
	}

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}
