package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/ada"
	mlog "github.com/rakunlabs/ada/middleware/log"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	mserver "github.com/rakunlabs/ada/middleware/server"
	"github.com/rytsh/gopkg/internal/site"
)

type Config struct {
	Address         string
	AdminToken      string
	FetchMissing    bool
	Version         string
	Commit          string
	RefreshInterval time.Duration
}

func Start(ctx context.Context, cfg Config, siteManager *site.Manager) error {
	server := ada.New(
		ada.WithLogger(slog.Default()),
		ada.WithShutdownTimeout(10*time.Second),
	)
	server.Use(
		mrecover.Middleware(),
		mserver.Middleware("gopkg"),
		mrequestid.Middleware(),
		mlog.Middleware(),
	)
	server.GET("/-/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	server.GET("/-/admin", requireAdmin(cfg.AdminToken, adminHandler(siteManager, cfg.RefreshInterval)))
	server.GET("/-/status", statusHandler(siteManager))
	server.POST("/-/reload", requireAdmin(cfg.AdminToken, reloadHandler(siteManager)))
	server.POST("/-/modules", requireAdmin(cfg.AdminToken, addModuleHandler(siteManager)))
	server.Handle("/", siteManager)
	server.HandleWildcard("/", siteManager)

	networkMode := "disabled"
	if cfg.FetchMissing {
		networkMode = "upstream-goproxy"
	}
	stats := siteManager.Stats()
	slog.InfoContext(ctx, "serving pkgsite",
		"version", cfg.Version,
		"commit", cfg.Commit,
		"url", browserURL(cfg.Address),
		"listen", cfg.Address,
		"local_modules", stats.LocalModules,
		"proxy_modules", stats.ProxyModules,
		"search_packages", stats.SearchPackages,
		"network", networkMode,
	)
	if err := server.StartWithContext(ctx, cfg.Address,
		ada.WithBaseContext(ctx),
		ada.WithReadHeaderTimeout(5*time.Second),
		ada.WithHTTPServerFunc(func(server *http.Server) *http.Server {
			server.IdleTimeout = 2 * time.Minute
			return server
		}),
	); err != nil {
		return err
	}
	return nil
}

var adminPage = template.Must(template.New("admin").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>gopkg administration</title>
  <style>
    body { max-width: 52rem; margin: 3rem auto; padding: 0 1rem; font: 16px/1.5 system-ui, sans-serif; color: #202124; }
    fieldset { margin: 1.5rem 0; padding: 1rem; border: 1px solid #dadce0; border-radius: .5rem; }
    label { display: block; margin-top: .75rem; font-weight: 600; }
    input { box-sizing: border-box; width: 100%; padding: .5rem; }
    button { margin-top: 1rem; padding: .55rem 1rem; }
    .message { padding: .75rem; background: #e6f4ea; border-radius: .4rem; }
    code { background: #f1f3f4; padding: .1rem .25rem; }
  </style>
</head>
<body>
  <h1>gopkg administration</h1>
  {{if .Message}}<p class="message">{{.Message}}</p>{{end}}
  <p>Loaded {{.Stats.ProxyModules}} proxy modules and {{.Stats.SearchPackages}} packages at {{.Stats.LoadedAt}}.</p>
  <p>The proxy directory is checked every {{.RefreshInterval}}. Rebuilds are atomic: requests continue using the previous index until the new index is ready.</p>
  <fieldset>
    <legend>Refresh proxy index</legend>
    <form method="post" action="/-/reload">
      <button type="submit">Refresh now</button>
    </form>
  </fieldset>
  <fieldset>
    <legend>Add a module version</legend>
    <p>Upload the three files from a standard Go module proxy. No outbound network request is made.</p>
    <form method="post" action="/-/modules" enctype="multipart/form-data">
      <label>Module path <input name="module" required placeholder="example.com/project"></label>
      <label>Version <input name="version" required placeholder="v1.2.3"></label>
      <label>.info file <input type="file" name="info" required></label>
      <label>.mod file <input type="file" name="mod" required></label>
      <label>.zip file <input type="file" name="zip" required></label>
      <button type="submit">Upload and refresh</button>
    </form>
  </fieldset>
  <p><a href="/">Return to package documentation</a></p>
</body>
</html>`))

func requireAdmin(token string, next http.Handler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.Error(w, "proxy administration is disabled", http.StatusNotFound)
			return
		}
		_, password, basicOK := r.BasicAuth()
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if (!basicOK || subtle.ConstantTimeCompare([]byte(password), []byte(token)) != 1) &&
			subtle.ConstantTimeCompare([]byte(bearer), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="gopkg administration", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func adminHandler(siteManager *site.Manager, refreshInterval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := adminPage.Execute(w, struct {
			Stats           site.Stats
			RefreshInterval time.Duration
			Message         string
		}{
			Stats:           siteManager.Stats(),
			RefreshInterval: refreshInterval,
			Message:         r.URL.Query().Get("message"),
		}); err != nil {
			slog.ErrorContext(r.Context(), "render admin page failed", "error", err)
		}
	}
}

func statusHandler(siteManager *site.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(siteManager.Stats())
	}
}

func reloadHandler(siteManager *site.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := siteManager.Reload(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		actionResponse(w, r, stats, "Proxy index refreshed", http.StatusOK)
	}
}

func addModuleHandler(siteManager *site.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 520<<20)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, fmt.Sprintf("parse upload: %v", err), http.StatusBadRequest)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		info, _, err := r.FormFile("info")
		if err != nil {
			http.Error(w, "info file is required", http.StatusBadRequest)
			return
		}
		defer info.Close()
		mod, _, err := r.FormFile("mod")
		if err != nil {
			http.Error(w, "mod file is required", http.StatusBadRequest)
			return
		}
		defer mod.Close()
		zipFile, _, err := r.FormFile("zip")
		if err != nil {
			http.Error(w, "zip file is required", http.StatusBadRequest)
			return
		}
		defer zipFile.Close()

		modulePath := strings.TrimSpace(r.FormValue("module"))
		version := strings.TrimSpace(r.FormValue("version"))
		stats, err := siteManager.AddVersion(r.Context(), modulePath, version, info, mod, zipFile)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		actionResponse(w, r, stats, fmt.Sprintf("Added %s@%s", modulePath, version), http.StatusCreated)
	}
}

func actionResponse(w http.ResponseWriter, r *http.Request, stats site.Stats, message string, status int) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/-/admin?message="+url.QueryEscape(message), http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Message string     `json:"message"`
		Stats   site.Stats `json:"stats"`
	}{Message: message, Stats: stats})
}

func browserURL(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "http://" + address
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}
