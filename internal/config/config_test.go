package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rakunlabs/chu"
)

func TestLoadAppliesFileThenEnvironment(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "gopkg.yaml")
	if err := os.WriteFile(configPath, []byte(`
http: ":7000"
proxy_dir:
  - /file/proxy
admin_token: file-secret
fetch_timeout: 45s
refresh: 1m
exclude:
  - "balbbla.com/**"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONFIG_FILE", configPath)
	t.Setenv("GOPKG_HTTP", "127.0.0.1:9000")
	t.Setenv("GOPKG_DIR", "/src/one,/src/two")
	t.Setenv("GOPKG_ADMIN_TOKEN", "env-secret")
	t.Setenv("GOPKG_UPSTREAM_PROXY", "https://user:proxy-secret@proxy.example.com")

	cfg, err := Load(context.Background(), "test")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTP != "127.0.0.1:9000" {
		t.Errorf("HTTP = %q, want environment value", cfg.HTTP)
	}
	if !reflect.DeepEqual(cfg.Dirs, []string{"/src/one", "/src/two"}) {
		t.Errorf("Dirs = %#v, want comma-separated environment values", cfg.Dirs)
	}
	if !reflect.DeepEqual(cfg.ProxyDirs, []string{"/file/proxy"}) {
		t.Errorf("ProxyDirs = %#v, want file value", cfg.ProxyDirs)
	}
	if cfg.AdminToken != "env-secret" {
		t.Errorf("AdminToken = %q, want environment value", cfg.AdminToken)
	}
	if !reflect.DeepEqual(cfg.Exclude, []string{"balbbla.com/**"}) {
		t.Errorf("Exclude = %#v, want file value", cfg.Exclude)
	}
	t.Setenv("GOPKG_EXCLUDE", "example.com/**,github.com/acme/legacy")
	envCfg, err := Load(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(envCfg.Exclude, []string{"example.com/**", "github.com/acme/legacy"}) {
		t.Errorf("Exclude = %#v, want environment override", envCfg.Exclude)
	}
	if cfg.FetchTimeout != 45*time.Second {
		t.Errorf("FetchTimeout = %s, want 45s", cfg.FetchTimeout)
	}
	if cfg.RefreshInterval != time.Minute {
		t.Errorf("RefreshInterval = %s, want 1m", cfg.RefreshInterval)
	}

	masked := chu.MarshalMap(cfg)
	logged, ok := masked.(map[string]any)
	if !ok {
		t.Fatalf("MarshalMap() type = %T, want map[string]any", masked)
	}
	if _, exists := logged["admin_token"]; exists {
		t.Error("MarshalMap() exposed admin_token")
	}
	if _, exists := logged["upstream_proxy"]; exists {
		t.Error("MarshalMap() exposed upstream_proxy credentials")
	}
}
