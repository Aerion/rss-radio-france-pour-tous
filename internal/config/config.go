// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	return Config{
		Port:                port,
		PublicBaseURL:       baseURL,
		RadioFranceAPIToken: token,
	}, nil
}
