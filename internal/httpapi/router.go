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

// Instrumenter wraps a handler with metrics/logging under a short,
// low-cardinality route name. Satisfied by *observability.Observability;
// declared here so this package doesn't need to import it directly.
type Instrumenter interface {
	Wrap(route string, h http.HandlerFunc) http.HandlerFunc
}

// Routes returns the app's HTTP handler, with every route wrapped by instr.
func (s *Server) Routes(instr Instrumenter) http.Handler {
	mux := http.NewServeMux()
	register := func(pattern, route string, h handlerFunc) {
		mux.HandleFunc(pattern, instr.Wrap(route, adapt(h)))
	}
	register("GET /{$}", "home", s.handleHome)
	register("GET /robots.txt", "robots", s.handleRobots)
	register("GET /search/", "search", s.handleSearch)
	register("GET /rss/{showId}", "rss", s.handleRSS)
	register("GET /audio/{manifestationId}", "audio", s.handleAudio)
	return mux
}
