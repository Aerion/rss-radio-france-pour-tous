package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/feedcache"
	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// noopInstrumenter satisfies Instrumenter without recording anything -
// these tests care about routing/handler behavior, not observability.
type noopInstrumenter struct{}

func (noopInstrumenter) Wrap(route string, h http.HandlerFunc) http.HandlerFunc { return h }

func newTestServer(t *testing.T, api API, audioResolver AudioResolver) http.Handler {
	t.Helper()
	return newTestServerWithBlockedUserAgents(t, api, audioResolver, nil)
}

func newTestServerWithBlockedUserAgents(t *testing.T, api API, audioResolver AudioResolver, blocked []string) http.Handler {
	t.Helper()
	return newServerForTest(t, api, audioResolver, feedcache.New(time.Hour, nil), &fakeEnrichmentStatus{}, blocked).Routes(noopInstrumenter{})
}

func newServerForTest(t *testing.T, api API, audioResolver AudioResolver, feedCache *feedcache.Cache, enrichmentStatus EnrichmentStatus, blocked []string) *Server {
	t.Helper()
	return NewServer(ServerConfig{
		API:               api,
		PublicBaseURL:     "https://radio-france-rss.example.com",
		AudioResolver:     audioResolver,
		FeedCache:         feedCache,
		EnrichmentStatus:  enrichmentStatus,
		BlockedUserAgents: blocked,
	})
}

func TestHandleRequest_UnknownRoute404(t *testing.T) {
	h := newTestServer(t, &fakeAPI{}, &fakeAudioResolver{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRequest_Robots(t *testing.T) {
	h := newTestServer(t, &fakeAPI{}, &fakeAudioResolver{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Disallow: /rss/") || !strings.Contains(body, "Disallow: /audio/") {
		t.Errorf("body = %q", body)
	}
}

func TestHandleRequest_Homepage(t *testing.T) {
	h := newTestServer(t, &fakeAPI{}, &fakeAudioResolver{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "RSS Radio France pour tous") {
		t.Error("homepage body missing expected title")
	}
}

func TestHandleRequest_RSSFeed(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	d := radiofrance.Diffusion{ID: "d1", Title: "Episode 1", CreatedTime: 1700000000}
	d.Relationships.Manifestations = []string{"m1"}

	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{
		Diffusions:  []radiofrance.Diffusion{d},
		ShowDetails: show,
	}}
	h := newTestServer(t, api, &fakeAudioResolver{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<rss") || !strings.Contains(body, "Affaires sensibles") {
		t.Errorf("body = %q", body)
	}
}

func TestHandleRequest_RSSFeed_UpstreamError(t *testing.T) {
	api := &fakeAPI{showDiffusionsErr: errors.New("upstream boom")}
	h := newTestServer(t, api, &fakeAudioResolver{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandleRequest_Search(t *testing.T) {
	api := &fakeAPI{searchResults: []radiofrance.SearchResult{
		{ShowID: "0b91efaf", Title: "Affaires sensibles", Path: "https://radiofrance.fr/affaires-sensibles"},
	}}
	h := newTestServer(t, api, &fakeAudioResolver{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search/?query=affaires+sensibles", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Affaires sensibles") || !strings.Contains(body, `"rssUrl"`) {
		t.Errorf("body = %q", body)
	}
}

func TestHandleRequest_Search_MissingQuery(t *testing.T) {
	h := newTestServer(t, &fakeAPI{}, &fakeAudioResolver{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search/", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRequest_Audio(t *testing.T) {
	resolver := &fakeAudioResolver{url: "https://cdn.example.com/audio.mp3", showID: "0b91efaf", showTitle: "Affaires sensibles"}
	h := newTestServer(t, &fakeAPI{}, resolver)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audio/301c6eb1-61d4-4120-8cd7-e415ffc4f7df", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://cdn.example.com/audio.mp3" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleRequest_Audio_UpstreamError(t *testing.T) {
	resolver := &fakeAudioResolver{err: errors.New("not found upstream")}
	h := newTestServer(t, &fakeAPI{}, resolver)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audio/nonexistent-id", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandleRequest_RSSFeed_BlockedUserAgent(t *testing.T) {
	h := newTestServerWithBlockedUserAgents(t, &fakeAPI{}, &fakeAudioResolver{}, []string{"gptbot"})
	req := httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 GPTBot/1.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandleRequest_Audio_BlockedUserAgent(t *testing.T) {
	h := newTestServerWithBlockedUserAgents(t, &fakeAPI{}, &fakeAudioResolver{}, []string{"gptbot"})
	req := httptest.NewRequest(http.MethodGet, "/audio/301c6eb1-61d4-4120-8cd7-e415ffc4f7df", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 GPTBot/1.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandleRequest_RSSFeed_NonBlockedUserAgentPasses(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{ShowDetails: show}}
	h := newTestServerWithBlockedUserAgents(t, api, &fakeAudioResolver{}, []string{"gptbot"})
	req := httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SomeBrowser/1.0)")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRequest_Homepage_BlockedUserAgentStillAllowed(t *testing.T) {
	h := newTestServerWithBlockedUserAgents(t, &fakeAPI{}, &fakeAudioResolver{}, []string{"gptbot"})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 GPTBot/1.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (blocklist only applies to feed routes)", rec.Code)
	}
}

func diffusionWithOneManifestation(id string) radiofrance.Diffusion {
	d := radiofrance.Diffusion{ID: id, Title: "Episode " + id, CreatedTime: 1700000000}
	d.Relationships.Manifestations = []string{"m-" + id}
	return d
}

func TestHandleRequest_RSSFeed_DegradedCacheHitStillPendingServesStaleWithoutRecalling(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{
		Diffusions: []radiofrance.Diffusion{diffusionWithOneManifestation("d1")}, ShowDetails: show,
	}}
	enrichment := &fakeEnrichmentStatus{allResolved: false}
	server := NewServer(ServerConfig{
		API: api, PublicBaseURL: "https://radio-france-rss.example.com", AudioResolver: &fakeAudioResolver{},
		FeedCache: feedcache.New(time.Hour, nil), EnrichmentStatus: enrichment,
	})
	h := server.Routes(noopInstrumenter{})

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if rec1.Code != http.StatusOK || rec2.Code != http.StatusOK {
		t.Fatalf("status codes = %d, %d, want 200, 200", rec1.Code, rec2.Code)
	}
	if api.showDiffusionsCalls != 1 {
		t.Errorf("api.showDiffusionsCalls = %d, want 1 (second request should be served from the feed cache, not rebuilt)", api.showDiffusionsCalls)
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Error("expected the cached response body to match the original")
	}
	// No manifestation resolver is configured, so every item's duration is
	// unresolved and the entry is cached degraded - the active-invalidation
	// check should have run exactly once, on the second (cache-hit) request.
	if enrichment.calls != 1 {
		t.Errorf("enrichment.calls = %d, want 1", enrichment.calls)
	}
}

func TestHandleRequest_RSSFeed_DegradedCacheHitButNowResolvedInvalidatesAndRebuilds(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{
		Diffusions: []radiofrance.Diffusion{diffusionWithOneManifestation("d1")}, ShowDetails: show,
	}}
	enrichment := &fakeEnrichmentStatus{allResolved: true}
	server := NewServer(ServerConfig{
		API: api, PublicBaseURL: "https://radio-france-rss.example.com", AudioResolver: &fakeAudioResolver{},
		FeedCache: feedcache.New(time.Hour, nil), EnrichmentStatus: enrichment,
	})
	h := server.Routes(noopInstrumenter{})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if api.showDiffusionsCalls != 2 {
		t.Errorf("api.showDiffusionsCalls = %d, want 2 (a degraded entry whose enrichment has since caught up should be invalidated and rebuilt)", api.showDiffusionsCalls)
	}
}

func TestHandleRequest_RSSFeed_NonDegradedCacheHitSkipsEnrichmentCheck(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{
		Diffusions: []radiofrance.Diffusion{diffusionWithOneManifestation("d1")}, ShowDetails: show,
	}}
	enrichment := &fakeEnrichmentStatus{}
	resolver := fakeManifestationResolver{url: "https://cdn.example.com/audio.mp3", duration: 90 * time.Second}
	server := NewServer(ServerConfig{
		API: api, PublicBaseURL: "https://radio-france-rss.example.com", ManifestationResolver: resolver,
		AudioResolver: &fakeAudioResolver{}, FeedCache: feedcache.New(time.Hour, nil), EnrichmentStatus: enrichment,
	})
	h := server.Routes(noopInstrumenter{})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if api.showDiffusionsCalls != 1 {
		t.Errorf("api.showDiffusionsCalls = %d, want 1 (fully-resolved entry should be served from cache)", api.showDiffusionsCalls)
	}
	if enrichment.calls != 0 {
		t.Errorf("enrichment.calls = %d, want 0 (active-invalidation check should be skipped for a non-degraded entry)", enrichment.calls)
	}
}

func TestHandleRequest_RSSFeed_NonDegradedCacheHitButExpiredInvalidatesAndRebuilds(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{
		Diffusions: []radiofrance.Diffusion{diffusionWithOneManifestation("d1")}, ShowDetails: show,
	}}
	expired := time.Now().Add(-time.Minute)
	resolver := fakeManifestationResolver{url: "https://cdn.example.com/audio.mp3", duration: 90 * time.Second, expiresAt: &expired}
	server := NewServer(ServerConfig{
		API: api, PublicBaseURL: "https://radio-france-rss.example.com", ManifestationResolver: resolver,
		AudioResolver: &fakeAudioResolver{}, FeedCache: feedcache.New(time.Hour, nil), EnrichmentStatus: &fakeEnrichmentStatus{},
	})
	h := server.Routes(noopInstrumenter{})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if api.showDiffusionsCalls != 2 {
		t.Errorf("api.showDiffusionsCalls = %d, want 2 (a fully-resolved entry whose baked-in URL has since expired should be invalidated and rebuilt)", api.showDiffusionsCalls)
	}
}

func TestHandleRequest_RSSFeed_NonDegradedCacheHitNotYetExpiredServesFromCache(t *testing.T) {
	show := radiofrance.Show{ID: "0b91efaf", Title: "Affaires sensibles"}
	api := &fakeAPI{showDiffusions: radiofrance.ShowDiffusions{
		Diffusions: []radiofrance.Diffusion{diffusionWithOneManifestation("d1")}, ShowDetails: show,
	}}
	notYetExpired := time.Now().Add(time.Hour)
	resolver := fakeManifestationResolver{url: "https://cdn.example.com/audio.mp3", duration: 90 * time.Second, expiresAt: &notYetExpired}
	server := NewServer(ServerConfig{
		API: api, PublicBaseURL: "https://radio-france-rss.example.com", ManifestationResolver: resolver,
		AudioResolver: &fakeAudioResolver{}, FeedCache: feedcache.New(time.Hour, nil), EnrichmentStatus: &fakeEnrichmentStatus{},
	})
	h := server.Routes(noopInstrumenter{})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rss/0b91efaf", nil))

	if api.showDiffusionsCalls != 1 {
		t.Errorf("api.showDiffusionsCalls = %d, want 1 (entry not yet expired should still be served from cache)", api.showDiffusionsCalls)
	}
}
