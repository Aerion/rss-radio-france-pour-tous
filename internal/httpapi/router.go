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
	Search(ctx context.Context, query string) ([]radiofrance.SearchResult, error)
}

// AudioResolver resolves a manifestation ID to its playable URL (and,
// where known, which show it belongs to) for the /audio/ redirect -
// typically backed by internal/episodecache, which consults a cache
// before the Radio France API. The route itself is legacy: feed
// enclosures now embed the resolved playable URL directly (see
// feed.ManifestationResolver), so /audio/ mainly still serves links
// embedded in feeds fetched before that change, plus the rare item whose
// URL couldn't be resolved at feed-build time.
type AudioResolver interface {
	ResolveAudioURL(ctx context.Context, manifestationID string) (url, showID, showTitle string, err error)
}

// Server holds the dependencies shared by all HTTP handlers.
type Server struct {
	api               API
	feedBuilder       feed.Builder
	audioResolver     AudioResolver
	publicBaseURL     string
	blockedUserAgents []string
}

// NewServer builds a Server. publicBaseURL is this app's own externally
// visible base URL (e.g. "https://rss.example.com"), used to build
// self-referencing links in the feed and search results. blockedUserAgents
// is a lowercased list of substrings (see config.Config.BlockedUserAgents);
// requests to feed-serving routes with a matching User-Agent get a 403.
func NewServer(api API, publicBaseURL string, manifestationResolver feed.ManifestationResolver, imageResolver feed.ImageResolver, descriptionResolver feed.DescriptionResolver, audioResolver AudioResolver, blockedUserAgents []string) *Server {
	return &Server{
		api:               api,
		feedBuilder:       feed.Builder{PublicBaseURL: publicBaseURL, Resolver: manifestationResolver, ImageResolver: imageResolver, DescriptionResolver: descriptionResolver},
		audioResolver:     audioResolver,
		publicBaseURL:     publicBaseURL,
		blockedUserAgents: blockedUserAgents,
	}
}

// Instrumenter wraps a handler under a short, low-cardinality route name -
// e.g. to record metrics/logs (*observability.Observability) or to
// log an analytics event (*analytics.Writer). Declared here so this
// package doesn't need to import either of those directly.
type Instrumenter interface {
	Wrap(route string, h http.HandlerFunc) http.HandlerFunc
}

// Routes returns the app's HTTP handler, with every route wrapped by each
// of instrs in turn (first one outermost).
func (s *Server) Routes(instrs ...Instrumenter) http.Handler {
	mux := http.NewServeMux()
	register := func(pattern, route string, h handlerFunc) {
		handler := adapt(h)
		for i := len(instrs) - 1; i >= 0; i-- {
			handler = instrs[i].Wrap(route, handler)
		}
		mux.HandleFunc(pattern, handler)
	}
	register("GET /{$}", "home", s.handleHome)
	register("GET /robots.txt", "robots", s.handleRobots)
	register("GET /search/", "search", s.handleSearch)
	register("GET /rss/{showId}", "rss", s.blockUserAgent(s.handleRSS))
	register("GET /audio/{manifestationId}", "audio", s.blockUserAgent(s.handleAudio))
	return mux
}
