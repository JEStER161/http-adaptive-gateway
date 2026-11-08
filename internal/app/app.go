package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"gateway/configs"
	"gateway/internal/api/balancer"
	httpdelivery "gateway/internal/api/delivery/http"
	"gateway/internal/api/observer"
	"gateway/internal/api/plugin"
	"gateway/internal/api/plugin/ratelimit"
	"gateway/internal/api/proxy"
	"gateway/internal/api/routing"
	httpserver "gateway/pkg/api/http"
)

type App struct{ Config *configs.Config }

func NewApp(config *configs.Config) *App { return &App{Config: config} }

func (a *App) Start(logger *slog.Logger) error {
	balancerRegistry := balancer.NewRegistry()
	pluginRegistry := plugin.NewRegistry()
	if err := pluginRegistry.Register(ratelimit.Factory{}); err != nil {
		return err
	}
	routes, err := routing.Compile(a.Config, balancerRegistry, pluginRegistry)
	if err != nil {
		return fmt.Errorf("compile routes: %w", err)
	}
	upstreamObserver := observer.NewLogging(logger)
	reverseProxy := proxy.New(a.Config.Proxy, a.Config.Logging.RequestIDFieldName, upstreamObserver, logger)
	defer reverseProxy.CloseIdleConnections()
	router := httpdelivery.NewRouter(logger, a.Config, routes, reverseProxy)
	server := httpserver.NewServer(router.Handler(), a.Config.Listen.HTTP)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "address", server.Addr, "strategy", a.Config.BalancerPlugin().Params.Strategy)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case serverErr := <-serverErrors:
		if !errors.Is(serverErr, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", serverErr)
		}
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.Config.Listen.HTTP.ShutdownTimeout.Duration)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	logger.Info("http server stopped")
	return nil
}
