// Command papergo-mcp serves PaperGo to MCP clients at /v1/mcp. It holds no
// credentials and no data: every tool call acts through the PaperGo API as
// the bearer credential the client sent.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"papergo/extensions/mcp"
	"papergo/extensions/mcp/builtin"
	"papergo/extensions/mcp/server"
	"papergo/internal/buildinfo"
)

// contributions are the compiled tool contributions a deployment may enable
// with PAPERGO_MCP_ENABLED_EXTENSIONS.
var contributions []mcp.Contribution

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.String())
		return
	}
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: papergo-mcp [--version]; configuration is read from PAPERGO_MCP_* environment variables")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(ctx, os.Getenv, log, nil); err != nil {
		log.Error("papergo-mcp stopped", "error", err.Error())
		os.Exit(1)
	}
}

// run serves until ctx ends, then drains requests in flight. ready, when
// set, receives the listening address.
func run(ctx context.Context, env func(string) string, log *slog.Logger, ready func(net.Addr)) error {
	cfg, tools, listen, enabled, err := configure(env)
	if err != nil {
		return err
	}
	cfg.Logger, cfg.Version = log, buildinfo.Version
	registry, err := mcp.NewRegistry(builtin.Tools(tools), contributions, enabled)
	if err != nil {
		return err
	}
	srv, err := server.New(cfg, registry)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: srv.RequestTimeout() + 5*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024,
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn)}
	log.Info("papergo-mcp listening", "address", listener.Addr().String(), "path", server.Path, "backend", cfg.BackendURL.String(),
		"version", buildinfo.Version, "tools", len(registry.Tools()))
	if ready != nil {
		ready(listener.Addr())
	}
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	// Stop admitting, then let requests in flight finish within their
	// deadline.
	shutdown, cancel := context.WithTimeout(context.Background(), srv.RequestTimeout())
	defer cancel()
	err = httpServer.Shutdown(shutdown)
	if served := <-served; !errors.Is(served, http.ErrServerClosed) && err == nil {
		err = served
	}
	log.Info("papergo-mcp stopped")
	return err
}

// configure reads the PAPERGO_MCP_* settings.
func configure(env func(string) string) (cfg server.Config, tools builtin.Options, listen string, enabled []string, err error) {
	fail := func(setting string, err error) (server.Config, builtin.Options, string, []string, error) {
		return cfg, tools, "", nil, fmt.Errorf("%s: %w", setting, err)
	}
	raw := env("PAPERGO_MCP_BACKEND_URL")
	if raw == "" {
		return cfg, tools, "", nil, errors.New("PAPERGO_MCP_BACKEND_URL is required: the PaperGo API's origin, such as http://papergo:8080")
	}
	if cfg.BackendURL, err = server.ParseBackendURL(raw); err != nil {
		return fail("PAPERGO_MCP_BACKEND_URL", err)
	}
	if raw := env("PAPERGO_MCP_PUBLIC_URL"); raw != "" {
		if cfg.PublicURL, err = server.ParsePublicURL(raw); err != nil {
			return fail("PAPERGO_MCP_PUBLIC_URL", err)
		}
		// One host usually serves the endpoint and the web app.
		tools.AppURL = server.AppOrigin(cfg.PublicURL)
	}
	if raw := env("PAPERGO_MCP_APP_URL"); raw != "" {
		if tools.AppURL, err = server.ParseAppURL(raw); err != nil {
			return fail("PAPERGO_MCP_APP_URL", err)
		}
	}
	if cfg.AllowedOrigins, err = server.ParseOrigins(env("PAPERGO_MCP_ALLOWED_ORIGINS")); err != nil {
		return fail("PAPERGO_MCP_ALLOWED_ORIGINS", err)
	}
	// The web app checks the connections it creates from the browser; the
	// public URL's origin is allowed already.
	if tools.AppURL != nil && (cfg.PublicURL == nil || *server.AppOrigin(cfg.PublicURL) != *server.AppOrigin(tools.AppURL)) {
		cfg.AllowedOrigins = append(cfg.AllowedOrigins, strings.ToLower(server.AppOrigin(tools.AppURL).String()))
	}
	listen = env("PAPERGO_MCP_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8081"
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return fail("PAPERGO_MCP_LISTEN", err)
	}
	enabled = strings.FieldsFunc(env("PAPERGO_MCP_ENABLED_EXTENSIONS"), func(r rune) bool { return r == ',' || r == ' ' })
	return cfg, tools, listen, enabled, nil
}
