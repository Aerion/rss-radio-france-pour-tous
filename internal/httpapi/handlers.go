package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/analytics"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/feedcache"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(homepageHTML)
	return err
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err := fmt.Fprint(w, "User-agent: *\nDisallow: /rss/\nDisallow: /audio/\n")
	return err
}

// searchResultJSON is the shape expected by the homepage's client-side
// search UI (internal/httpapi/web/index.html).
type searchResultJSON struct {
	Title      string `json:"title"`
	Path       string `json:"path"`
	Standfirst string `json:"standfirst"`
	ImgURL     string `json:"imgUrl"`
	RSSURL     string `json:"rssUrl"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, "Missing query parameter", http.StatusBadRequest)
		return nil
	}

	results, err := s.api.Search(r.Context(), query)
	if err != nil {
		return err
	}

	out := make([]searchResultJSON, len(results))
	for i, res := range results {
		out[i] = searchResultJSON{
			Title:      res.Title,
			Path:       res.Path,
			Standfirst: res.Standfirst,
			ImgURL:     res.ImgURL,
			RSSURL:     s.publicBaseURL + "/rss/" + res.ShowID,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// observeShow records the show an /rss/ request resolved to, both for the
// analytics event Writer.Wrap eventually inserts (see analytics.WithShow)
// and, if configured, the show_feed_requests_total metric.
func (s *Server) observeShow(ctx context.Context, showID, showTitle string) {
	analytics.WithShow(ctx, showID, showTitle)
	if s.showObserver != nil {
		s.showObserver.ObserveShowRequest(ctx, showID)
	}
}

func (s *Server) handleRSS(w http.ResponseWriter, r *http.Request) error {
	showID := r.PathValue("showId")

	page := 0
	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}

	body, err := s.resolveFeed(r.Context(), showID, page, r.URL.Path)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, err = fmt.Fprint(w, body)
	return err
}

// resolveFeed returns the rendered feed XML for showID's page, consulting
// the feed cache first (see feedcache.Cache) and only falling through to a
// real rebuild - fetching diffusions, building the feed, and repopulating
// the cache - on a miss or an actively-invalidated entry. requestPath is
// r.URL.Path, needed to build the page's next-page link.
//
// Also attributes the request to its show for analytics/metrics (see
// observeShow) as soon as the show is known: immediately on a cache hit,
// or right after the upstream fetch on a miss - deliberately before the
// (possibly slower) feed build, so a request that fails partway through
// building still gets attributed rather than showing up as show-less.
func (s *Server) resolveFeed(ctx context.Context, showID string, page int, requestPath string) (body string, err error) {
	key := feedcache.Key(showID, page)
	if entry, ok := s.feedCache.Get(ctx, key); ok {
		// A page cached degraded (some items still missing enrichment)
		// stays fresh from the feed cache's point of view until its TTL
		// elapses - but if background enrichment has since caught up,
		// there's no reason to keep serving the stale, degraded copy
		// until then: invalidate it now and fall through to rebuild, this
		// time fully enriched. Separately, even a page that was fully
		// resolved when cached can have a baked-in enclosure URL that's
		// since expired - EarliestExpiry catches that case too, since
		// nothing else about a fully-resolved entry would otherwise ever
		// trigger a rebuild before ttl.
		degradedNowResolved := entry.HadDegraded && s.enrichmentStatus.AllResolved(entry.Diffusions)
		expired := entry.EarliestExpiry != nil && !entry.EarliestExpiry.After(time.Now())
		if !degradedNowResolved && !expired {
			s.observeShow(ctx, entry.ShowID, entry.ShowTitle)
			return entry.Body, nil
		}
		s.feedCache.Invalidate(key)
	}

	showDiffusions, err := s.api.GetShowDiffusions(ctx, showID, page)
	if err != nil {
		return "", err
	}
	s.observeShow(ctx, showID, showDiffusions.ShowDetails.Title)

	var nextPageURL string
	if showDiffusions.NextPageIdx != nil {
		nextPageURL = fmt.Sprintf("%s%s?page=%d", s.publicBaseURL, requestPath, *showDiffusions.NextPageIdx)
	}

	body, hadDegraded, earliestExpiry, err := s.feedBuilder.Build(ctx, showDiffusions, nextPageURL)
	if err != nil {
		return "", err
	}

	s.feedCache.Set(key, feedcache.Entry{
		Body:           body,
		ShowID:         showID,
		ShowTitle:      showDiffusions.ShowDetails.Title,
		Diffusions:     showDiffusions.Diffusions,
		HadDegraded:    hadDegraded,
		EarliestExpiry: earliestExpiry,
	})

	return body, nil
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) error {
	manifestationID := r.PathValue("manifestationId")

	mp3URL, showID, showTitle, err := s.audioResolver.ResolveAudioURL(r.Context(), manifestationID)
	if err != nil {
		return err
	}
	if showID != "" {
		// Backfills the show_id Phase 3 otherwise leaves NULL on /audio/
		// analytics rows - populated once the corresponding show's feed has
		// been built at least once (see episodecache.Resolver.Resolve).
		analytics.WithShow(r.Context(), showID, showTitle)
	}

	http.Redirect(w, r, mp3URL, http.StatusFound)
	return nil
}
