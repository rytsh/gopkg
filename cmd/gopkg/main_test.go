package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigFetchMissing(t *testing.T) {
	for _, test := range []struct {
		name         string
		unsetGoProxy bool
		file         string
		upstream     string
		goProxy      string
		fetchMissing string
		args         []string
		wantUpstream string
	}{
		{name: "no upstream", unsetGoProxy: true},
		{name: "empty GOPROXY"},
		{name: "empty upstream", upstream: " ", goProxy: " \t"},
		{name: "environment off", goProxy: " off "},
		{name: "configured off overrides environment", upstream: " off ", goProxy: "https://fallback.example.com"},
		{name: "environment upstream", goProxy: " https://fallback.example.com ", wantUpstream: "https://fallback.example.com"},
		{name: "configured upstream overrides off", upstream: " https://proxy.example.com ", goProxy: "off", wantUpstream: "https://proxy.example.com"},
		{name: "configured upstream takes precedence", upstream: "https://proxy.example.com", goProxy: "https://fallback.example.com", wantUpstream: "https://proxy.example.com"},
		{name: "file disabled", file: "fetch_missing: false\n", upstream: "https://proxy.example.com"},
		{name: "environment disabled", fetchMissing: "false", goProxy: "https://fallback.example.com"},
		{name: "flag disabled", args: []string{"-fetch-missing=false"}, upstream: "https://proxy.example.com"},
		{name: "flag enabled without upstream", args: []string{"-fetch-missing"}},
		{name: "flag overrides disabled", fetchMissing: "false", args: []string{"-fetch-missing", "-upstream-proxy=https://proxy.example.com"}, goProxy: "off", wantUpstream: "https://proxy.example.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "gopkg.yaml")
			content := test.file
			if content == "" {
				content = "{}\n"
			}
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CONFIG_FILE_GOPKG", configPath)
			t.Setenv("GOPKG_UPSTREAM_PROXY", test.upstream)
			t.Setenv("GOPKG_FETCH_MISSING", test.fetchMissing)
			if test.fetchMissing == "" {
				if err := os.Unsetenv("GOPKG_FETCH_MISSING"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("GOPROXY", test.goProxy)
			if test.unsetGoProxy {
				if err := os.Unsetenv("GOPROXY"); err != nil {
					t.Fatal(err)
				}
			}

			cfg, _, err := loadConfig(context.Background(), test.args)
			if err != nil {
				t.Fatalf("loadConfig() error = %v", err)
			}
			if cfg.UpstreamProxy != test.wantUpstream {
				t.Errorf("UpstreamProxy = %q, want %q", cfg.UpstreamProxy, test.wantUpstream)
			}
			if wantFetch := test.wantUpstream != ""; cfg.FetchMissing != wantFetch {
				t.Errorf("FetchMissing = %t, want %t", cfg.FetchMissing, wantFetch)
			}
		})
	}
}

func TestLoadConfigFetchMode(t *testing.T) {
	for _, test := range []struct {
		name, file, env, want string
		args                  []string
		invalid               bool
	}{
		{name: "default", want: "download"},
		{name: "file", file: "fetch_mode: shared\n", want: "shared"},
		{name: "environment", env: "shared", want: "shared"},
		{name: "flag overrides environment", env: "shared", args: []string{"-fetch-mode=download"}, want: "download"},
		{name: "shared flag", args: []string{"-fetch-mode=shared"}, want: "shared"},
		{name: "invalid even without upstream", env: "invalid", invalid: true},
		{name: "invalid flag", args: []string{"-fetch-mode=invalid"}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "gopkg.yaml")
			content := test.file
			if content == "" {
				content = "{}\n"
			}
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CONFIG_FILE_GOPKG", configPath)
			t.Setenv("GOPKG_FETCH_MODE", test.env)
			if test.env == "" {
				if err := os.Unsetenv("GOPKG_FETCH_MODE"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("GOPKG_UPSTREAM_PROXY", "")
			t.Setenv("GOPROXY", "off")
			cfg, _, err := loadConfig(t.Context(), test.args)
			if test.invalid {
				if err == nil || !strings.Contains(err.Error(), "invalid fetch mode") {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.FetchMode != test.want || cfg.FetchMissing {
				t.Fatalf("mode=%q fetch_missing=%t", cfg.FetchMode, cfg.FetchMissing)
			}
		})
	}
}
