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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/analytics"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/config"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/episodecache"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/feedcache"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/httpapi"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/observability"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

const (
	serviceName = "radio-france-rss"

	analyticsBufferSize = 1000
	analyticsWorkers    = 2

	requestLogRetention        = 90 * 24 * time.Hour
	requestLogRetentionCadence = time.Hour
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	obs, err := observability.New(serviceName)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := obs.Shutdown(shutdownCtx); err != nil {
			slog.Error("error shutting down observability providers", "error", err)
		}
	}()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	analyticsWriter := analytics.NewWriter(pool, obs, analyticsBufferSize, analyticsWorkers)
	go analyticsWriter.RunRetention(ctx, requestLogRetention, requestLogRetentionCadence)

	client := radiofrance.NewClient(http.DefaultClient, cfg.RadioFranceAPIToken, obs)

	enricher := episodecache.NewEnricher(cfg.EnrichmentQueueSize, cfg.EnrichmentJobTimeout, obs)
	episodeCache := episodecache.NewResolver(episodecache.NewStore(pool), client, obs, enricher, cfg.ManifestationCacheMaxAge)
	go enricher.Run(ctx, episodeCache, cfg.EnrichmentWorkers)

	feedCache := feedcache.New(cfg.FeedCacheTTL, obs)
	go feedCache.Sweep(ctx, cfg.FeedCacheSweepInterval)

	server := httpapi.NewServer(httpapi.ServerConfig{
		API:                   client,
		PublicBaseURL:         cfg.PublicBaseURL,
		ManifestationResolver: episodeCache,
		ImageResolver:         episodeCache,
		DescriptionResolver:   episodeCache,
		AudioResolver:         episodeCache,
		FeedCache:             feedCache,
		EnrichmentStatus:      episodeCache,
		ShowObserver:          obs,
		BlockedUserAgents:     cfg.BlockedUserAgents,
	})

	mux := http.NewServeMux()
	mux.Handle("/", server.Routes(obs, analyticsWriter))
	mux.Handle("GET /metrics", obs.Handler())

	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

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
