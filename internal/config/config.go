package config

import (
	"context"
	"log/slog"
	"time"

	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/chu/loader/loaderenv"

	// External chu loaders (registered via init) so they can be selected by
	// name when the env-provided config set enables them.
	_ "github.com/rakunlabs/chu/loader/external/loaderawssecrets"
	_ "github.com/rakunlabs/chu/loader/external/loaderawsssm"
	_ "github.com/rakunlabs/chu/loader/external/loaderazurekeyvault"
	_ "github.com/rakunlabs/chu/loader/external/loaderconsul"
	_ "github.com/rakunlabs/chu/loader/external/loadergcpparameter"
	_ "github.com/rakunlabs/chu/loader/external/loadergcpsecret"
	_ "github.com/rakunlabs/chu/loader/external/loadervault"
)

const ServiceName = "gopkg"

type Config struct {
	HTTP            string        `cfg:"http" default:":8080"`
	Dirs            []string      `cfg:"dir"`
	ProxyDirs       []string      `cfg:"proxy_dir"`
	Exclude         []string      `cfg:"exclude"`
	AdminToken      string        `cfg:"admin_token" log:"-"`
	FetchMissing    bool          `cfg:"fetch_missing" default:"true"`
	FetchMode       string        `cfg:"fetch_mode" default:"download"`
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
