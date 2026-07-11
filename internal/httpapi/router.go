// Package httpapi is the HTTP layer: routing and request handlers.
package httpapi

import (
	"context"
	"net/http"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/feed"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// API is the subset of radiofrance.Client's behavior the HTTP layer needs.
// Handlers depend on this interface rather than the concrete client so
// they can be tested with a fake implementation.
type API interface {
	GetShowDiffusions(ctx context.Context, showID string, page int) (radiofrance.ShowDiffusions, error)
	GetManifestationURL(ctx context.Context, manifestationID string) (string, error)
	Search(ctx context.Context, query string) ([]radiofrance.SearchResult, error)
}

// Server holds the dependencies shared by all HTTP handlers.
type Server struct {
	api           API
	feedBuilder   feed.Builder
	publicBaseURL string
}

// NewServer builds a Server. publicBaseURL is this app's own externally
// visible base URL (e.g. "https://rss.example.com"), used to build
// self-referencing links in the feed and search results.
func NewServer(api API, publicBaseURL string) *Server {
	return &Server{
		api:           api,
		feedBuilder:   feed.Builder{PublicBaseURL: publicBaseURL},
		publicBaseURL: publicBaseURL,
	}
}

// Routes returns the app's HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", adapt(s.handleHome))
	mux.HandleFunc("GET /robots.txt", adapt(s.handleRobots))
	mux.HandleFunc("GET /search/", adapt(s.handleSearch))
	mux.HandleFunc("GET /rss/{showId}", adapt(s.handleRSS))
	mux.HandleFunc("GET /audio/{manifestationId}", adapt(s.handleAudio))
	return mux
}
