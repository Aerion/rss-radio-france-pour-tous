// Command server runs the Radio France RSS HTTP server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/config"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/httpapi"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := run(); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	client := radiofrance.NewClient(nil, cfg.RadioFranceAPIToken)
	server := httpapi.NewServer(client, cfg.PublicBaseURL)

	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: server.Routes(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", httpServer.Addr, "publicBaseURL", cfg.PublicBaseURL)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
