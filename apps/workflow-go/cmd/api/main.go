package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	backend "github.com/dataflow-poc/workflow-go/internal/api"
	"github.com/dataflow-poc/workflow-go/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	server, err := backend.New(ctx, config.Load())
	if err != nil {
		slog.Error("initialize API", "error", err)
		os.Exit(1)
	}
	defer server.Close()
	server.StartBackground(ctx)
	httpServer := &http.Server{Addr: net.JoinHostPort(os.Getenv("API_HOST"), server.Config.APIPort), Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	slog.Info("API started", "port", server.Config.APIPort)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}
