// Package httpapi is the HTTP layer: routing and request handlers.
package httpapi

import (
	"context"
	"net/http"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/feed"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/feedcache"
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

// EnrichmentStatus reports whether every diffusion on a cached feed page
// (and, for reruns, its origin) has finished background enrichment - lets
// a degraded cached page be invalidated as soon as it catches up, instead
// of waiting out the feed cache's TTL. Typically backed by
// *episodecache.Resolver. Declared here (rather than importing
// episodecache directly) for the same reason as AudioResolver: so this
// package depends only on the narrow interface it needs.
type EnrichmentStatus interface {
	AllResolved(diffusions []radiofrance.Diffusion) bool
}

// ShowObserver records that an /rss/ request resolved to a given show, for
// a per-show request-rate metric. show_id is deliberately its own metric
// label rather than folded into the low-cardinality "route" label used
// elsewhere (see Instrumenter/observability.Wrap) - the number of distinct
// Radio France shows is small and stable enough that a label per show is
// fine; the friendly title is looked up separately (see the shows table,
// populated by internal/analytics.Writer) rather than carried here, to keep
// this metric's cardinality and this interface minimal. Defined here rather
// than in a metrics package so this package depends only on the narrow
// interface it needs; observability.Observability implements it.
type ShowObserver interface {
	ObserveShowRequest(ctx context.Context, showID string)
}

// Server holds the dependencies shared by all HTTP handlers.
type Server struct {
	api               API
	feedBuilder       feed.Builder
	audioResolver     AudioResolver
	feedCache         *feedcache.Cache
	enrichmentStatus  EnrichmentStatus
	showObserver      ShowObserver
	publicBaseURL     string
	blockedUserAgents []string
}

// NewServer builds a Server. publicBaseURL is this app's own externally
// visible base URL (e.g. "https://rss.example.com"), used to build
// self-referencing links in the feed and search results. showObserver may
// be nil to skip the per-show request metric. blockedUserAgents is a
// lowercased list of substrings (see config.Config.BlockedUserAgents);
// requests to feed-serving routes with a matching User-Agent get a 403.
func NewServer(api API, publicBaseURL string, manifestationResolver feed.ManifestationResolver, imageResolver feed.ImageResolver, descriptionResolver feed.DescriptionResolver, audioResolver AudioResolver, feedCache *feedcache.Cache, enrichmentStatus EnrichmentStatus, showObserver ShowObserver, blockedUserAgents []string) *Server {
	return &Server{
		api:               api,
		feedBuilder:       feed.Builder{PublicBaseURL: publicBaseURL, Resolver: manifestationResolver, ImageResolver: imageResolver, DescriptionResolver: descriptionResolver},
		audioResolver:     audioResolver,
		feedCache:         feedCache,
		enrichmentStatus:  enrichmentStatus,
		showObserver:      showObserver,
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
