// Command redactor runs a local HTTP proxy that redacts PII from AI provider
// requests before they leave the machine and restores it in the responses.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/local/redactor/internal/config"
	"github.com/local/redactor/internal/handler"
	"github.com/local/redactor/internal/health"
	"github.com/local/redactor/internal/logging"
	"github.com/local/redactor/internal/pii"
	"github.com/local/redactor/internal/proxy"
	"github.com/local/redactor/internal/rules"
	"github.com/local/redactor/internal/ui"
)

// defaultConfigFile is where the configuration API writes when the process was
// started without a config file. Saving still requires an explicit PUT, so
// normal runs never create the file.
const defaultConfigFile = "config.toml"

func main() {
	configPath := flag.String("config", "", "path to TOML config file")
	listen := flag.String("listen", "", "listen address (overrides config)")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	logging.Setup(*logLevel)

	configFile := *configPath
	if configFile == "" {
		configFile = findLocalConfig()
	}

	var cfg *config.Config
	if configFile != "" {
		var err error
		cfg, err = config.Load(configFile)
		if err != nil {
			slog.Error("failed to load config", "path", configFile, "error", err)
			os.Exit(1)
		}
		if *configPath == "" {
			slog.Info("loaded config from current directory", "path", configFile)
		}
	} else {
		cfg = config.DefaultConfig()
		// The configuration API saves to this path on its first PUT, so give
		// it a destination even when the process started on defaults.
		configFile = defaultConfigFile
		slog.Info("no config file found, using defaults", "save_path", configFile)
	}

	if *listen != "" {
		cfg.Listen.Address = *listen
	}

	registry, err := pii.NewRegistry(cfg)
	if err != nil {
		slog.Error("failed to build PII registry", "error", err)
		os.Exit(1)
	}

	adapter := handler.NewAdapter(registry.Redactor(), registry.Unredactor(), cfg.PII.LogRedacted, cfg.PII.LogBodies)

	router := proxy.NewRouter()
	for name, upstream := range cfg.Upstream {
		if upstream.HostMatch != "" && upstream.BaseURL != "" {
			router.WithProvider(upstream.HostMatch, upstream.BaseURL)
			slog.Info("registered upstream", "name", name, "host", upstream.HostMatch, "url", upstream.BaseURL)
		}
	}

	forwarder := proxy.NewForwarder()
	proxyHandler := proxy.NewHandler(router, forwarder, []proxy.RequestMutator{adapter}, []proxy.ResponseMutator{adapter})

	api := rules.NewHandler(cfg, configFile, registry, adapter)

	server := proxy.NewServer(cfg.Listen.Address, newMux(proxyHandler, cfg, api))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("starting redactor proxy", "address", cfg.Listen.Address)
	if err := server.Start(ctx); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
	slog.Info("server shut down cleanly")
}

// findLocalConfig checks for config.toml in the current directory.
func findLocalConfig() string {
	path := filepath.Join(".", "config.toml")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// newMux builds the proxy's HTTP mux: /healthz answers liveness checks, the
// configuration API is mounted under /api/config when api is non-nil, the SPA
// (log viewer and configuration editor) plus the log event stream are mounted
// under /logs and /config when cfg.UI.Enable is set, and every other path is
// handled by proxyHandler. It is factored out so tests can exercise the exact
// routing the binary installs.
//
// The /api and UI endpoints are registered before the "/" catch-all because the
// mux resolves the most specific pattern first. Within /logs the exact
// "/logs/events" pattern wins over the "/logs/" subtree, so the SSE stream is
// never shadowed by the SPA fallback.
func newMux(proxyHandler http.Handler, cfg *config.Config, api *rules.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler())
	if api != nil {
		mux.HandleFunc("/api/config", api.Config)
		mux.HandleFunc("/api/config/reload", api.Reload)
		mux.HandleFunc("/api/config/preview", api.Preview)
	}
	if cfg != nil && cfg.UI.Enable {
		mux.Handle("/logs/events", logging.SSEHandler())
		mux.Handle("/logs/", ui.Handler(ui.LogsMountPath))
		mux.Handle("/logs", ui.Handler(ui.LogsMountPath))
		mux.Handle("/config/", ui.Handler(ui.ConfigMountPath))
		mux.Handle("/config", ui.Handler(ui.ConfigMountPath))
	}
	mux.Handle("/", proxyHandler)
	return mux
}
