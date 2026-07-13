// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the server.
type Config struct {
	// Port the HTTP server listens on.
	Port string

	// PublicBaseURL is the externally visible base URL used to build
	// self-referencing links (RSS enclosures, pagination, search UI).
	PublicBaseURL string

	// RadioFranceAPIToken is the x-token header value required by the
	// Radio France mobile API.
	RadioFranceAPIToken string

	// DatabaseURL is the Postgres connection string for the analytics
	// request log.
	DatabaseURL string

	// BlockedUserAgents is a lowercased list of substrings; any request to
	// a feed-serving route whose User-Agent header contains one of these
	// is rejected. Empty means no blocking.
	BlockedUserAgents []string

	// FeedCacheTTL is how long a rendered feed page is served from the
	// in-memory feed cache before a plain rebuild.
	FeedCacheTTL time.Duration

	// FeedCacheSweepInterval is how often the feed cache's background
	// sweep evicts expired entries.
	FeedCacheSweepInterval time.Duration

	// ManifestationCacheMaxAge bounds how long a cached manifestation URL
	// is trusted when Radio France doesn't report its own expiration.
	ManifestationCacheMaxAge time.Duration

	// EnrichmentQueueSize is how many pending enrichment jobs the queue
	// holds before new ones are dropped.
	EnrichmentQueueSize int

	// EnrichmentWorkers is the enrichment worker pool size - the dial that
	// keeps upstream concurrency low regardless of request volume.
	EnrichmentWorkers int

	// EnrichmentJobTimeout bounds how long a single enrichment job's
	// upstream call(s) may take.
	EnrichmentJobTimeout time.Duration
}

// Load reads configuration from environment variables, applying defaults
// where sensible and returning an error if a required variable is missing.
func Load() (Config, error) {
	token := os.Getenv("RADIOFRANCE_API_TOKEN")
	if token == "" {
		return Config{}, fmt.Errorf("RADIOFRANCE_API_TOKEN environment variable is required")
	}

	baseURL := os.Getenv("PUBLIC_BASE_URL")
	if baseURL == "" {
		return Config{}, fmt.Errorf("PUBLIC_BASE_URL environment variable is required")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	feedCacheTTL, err := durationEnv("FEED_CACHE_TTL", time.Hour)
	if err != nil {
		return Config{}, err
	}
	feedCacheSweepInterval, err := durationEnv("FEED_CACHE_SWEEP_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	manifestationCacheMaxAge, err := durationEnv("MANIFESTATION_CACHE_MAX_AGE", 30*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	enrichmentQueueSize, err := intEnv("ENRICHMENT_QUEUE_SIZE", 500)
	if err != nil {
		return Config{}, err
	}
	enrichmentWorkers, err := intEnv("ENRICHMENT_WORKERS", 2)
	if err != nil {
		return Config{}, err
	}
	enrichmentJobTimeout, err := durationEnv("ENRICHMENT_JOB_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Port:                     port,
		PublicBaseURL:            baseURL,
		RadioFranceAPIToken:      token,
		DatabaseURL:              databaseURL,
		BlockedUserAgents:        parseBlockedUserAgents(os.Getenv("BLOCKED_USER_AGENTS")),
		FeedCacheTTL:             feedCacheTTL,
		FeedCacheSweepInterval:   feedCacheSweepInterval,
		ManifestationCacheMaxAge: manifestationCacheMaxAge,
		EnrichmentQueueSize:      enrichmentQueueSize,
		EnrichmentWorkers:        enrichmentWorkers,
		EnrichmentJobTimeout:     enrichmentJobTimeout,
	}, nil
}

// durationEnv reads name as a time.Duration (e.g. "1h", "15s"), returning
// def if the variable is unset.
func durationEnv(name string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", name, raw, err)
	}
	return d, nil
}

// intEnv reads name as an int, returning def if the variable is unset.
func intEnv(name string, def int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", name, raw, err)
	}
	return n, nil
}

// parseBlockedUserAgents splits a comma-separated list of substrings,
// trimming whitespace and lowercasing each entry for case-insensitive
// matching, and dropping any empty entries (e.g. from a trailing comma).
func parseBlockedUserAgents(raw string) []string {
	var blocked []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry != "" {
			blocked = append(blocked, entry)
		}
	}
	return blocked
}
