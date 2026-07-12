// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strings"
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

	return Config{
		Port:                port,
		PublicBaseURL:       baseURL,
		RadioFranceAPIToken: token,
		DatabaseURL:         databaseURL,
		BlockedUserAgents:   parseBlockedUserAgents(os.Getenv("BLOCKED_USER_AGENTS")),
	}, nil
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
