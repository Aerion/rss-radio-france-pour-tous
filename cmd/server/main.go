// Command server runs the Radio France RSS HTTP server.
package main

import (
	"log/slog"
	"os"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("starting server", "port", cfg.Port)
}
