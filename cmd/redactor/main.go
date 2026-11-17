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
)

func main() {
	configPath := flag.String("config", "", "path to TOML config file")
	listen := flag.String("listen", "", "listen address (overrides config)")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	logging.Setup(*logLevel)

	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.Load(*configPath)
		if err != nil {
			slog.Error("failed to load config", "path", *configPath, "error", err)
			os.Exit(1)
		}
	} else if localConfig := findLocalConfig(); localConfig != "" {
		var err error
		cfg, err = config.Load(localConfig)
		if err != nil {
			slog.Error("failed to load config", "path", localConfig, "error", err)
			os.Exit(1)
		}
		slog.Info("loaded config from current directory", "path", localConfig)
	} else {
		cfg = config.DefaultConfig()
	}

	if *listen != "" {
		cfg.Listen.Address = *listen
	}

	registry, err := pii.NewRegistry(cfg)
	if err != nil {
		slog.Error("failed to build PII registry", "error", err)
		os.Exit(1)
	}

	adapter := handler.NewAdapter(registry.Redactor(), registry.Unredactor())

	router := proxy.NewRouter()
	for name, upstream := range cfg.Upstream {
		if upstream.HostMatch != "" && upstream.BaseURL != "" {
			router.WithProvider(upstream.HostMatch, upstream.BaseURL)
			slog.Info("registered upstream", "name", name, "host", upstream.HostMatch, "url", upstream.BaseURL)
		}
	}

	forwarder := proxy.NewForwarder()
	proxyHandler := proxy.NewHandler(router, forwarder, []proxy.RequestMutator{adapter}, []proxy.ResponseMutator{adapter})

	server := proxy.NewServer(cfg.Listen.Address, newMux(proxyHandler))

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

// newMux builds the proxy's HTTP mux: /healthz answers liveness checks and
// every other path is handled by proxyHandler. It is factored out so tests can
// exercise the exact routing the binary installs.
func newMux(proxyHandler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Handler())
	mux.Handle("/", proxyHandler)
	return mux
}