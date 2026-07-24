// Package feedcache is a Postgres-backed, TTL-based cache of fully-rendered
// feed pages, keyed by show ID and page number. It exists so a request for
// a show whose episode cache is already warm never re-renders the feed
// from scratch, and - together with internal/episodecache's enrichment
// queue - so a request for a *cold* show never blocks on a Radio France
// call: the feed is built once with whatever's already known, cached, and
// served as-is (possibly with some items still missing duration/artwork)
// until either its TTL elapses or background enrichment catches up (see
// Entry.HadDegraded and the active-invalidation check in internal/httpapi).
//
// Postgres-backed rather than in-memory: this is idle-RAM cost for no real
// benefit, since a Postgres round trip only adds a few milliseconds over a
// map lookup, and it puts feed_cache on the same footing as this app's
// other caches (manifestation_cache, diffusion_origin_image_cache).
//
// Page 0 (a show's newest episodes) is the only page kept fresh by wall-clock
// TTL. Radio France's own pagination is a raw offset over a newest-first
// list with no local re-sorting, so publishing any new episode shifts every
// deeper page's content by one slot - a blind per-page TTL would eventually
// serve a deeper page that's silently missing/duplicating an episode versus
// what upstream would return for that same page number "right now". Deeper
// pages are instead validated against page 0's current first diffusion ID
// (see Row.Page0AnchorID and Cache.Get): as long as nothing new has landed
// on page 0 since a deeper page was rendered, that page's content is still
// exactly correct, so it's served indefinitely; the moment page 0 changes,
// it's rebuilt.
package feedcache

import (
	"context"
	"log/slog"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// Observer receives the outcome of each feed cache lookup and the current
// entry count, for cache-effectiveness monitoring. Defined here rather than
// in a metrics package so this package stays decoupled from any particular
// metrics backend; observability.Observability implements it.
type Observer interface {
	ObserveFeedCacheLookup(ctx context.Context, outcome string)
	// SetFeedCacheEntries reports the current total number of rows held in
	// the feed cache.
	SetFeedCacheEntries(ctx context.Context, count int64)
}

const (
	outcomeHit  = "hit"
	outcomeMiss = "miss"
)

// Entry is one cached, fully-rendered feed page.
type Entry struct {
	// Body is the rendered feed XML.
	Body string
	// ShowID/ShowTitle are needed on a cache hit to still attribute the
	// request to the right show for analytics (see analytics.WithShow) -
	// a cache hit never calls the Radio France API, which is otherwise
	// where that information comes from.
	ShowID, ShowTitle string
	// Diffusions is the page's diffusions as returned by
	// radiofrance.Client.GetShowDiffusions, needed for active invalidation
	// (see httpapi.EnrichmentStatus.AllResolved).
	Diffusions []radiofrance.Diffusion
	// HadDegraded is whether any item in Body was still missing
	// enrichment (unresolved duration, or - for a rerun - an unresolved
	// origin) at the time this entry was cached.
	HadDegraded bool
	// EarliestExpiry is the earliest known expiration among Body's baked-in
	// enclosure URLs, or nil if none carry a known expiration (see
	// feed.Builder.Build). Lets a *fully*-resolved entry still be actively
	// invalidated once an enclosure it already resolved goes dead, rather
	// than serving it verbatim for the rest of ttl regardless - see the
	// invalidation check in internal/httpapi.
	EarliestExpiry *time.Time
}

// Row is one persisted feed-cache row, as stored by a store implementation.
// Exported so out-of-package test helpers (see MemStore) and alternative
// store implementations can be built without reaching into feedcache's
// internals.
type Row struct {
	Entry
	// FirstDiffusionID is the ID of this page's first diffusion (empty if
	// the page has none), used as the page-0 anchor other pages are
	// validated against - see Page0AnchorID.
	FirstDiffusionID string
	// Page0AnchorID is the show's page-0 FirstDiffusionID at the time this
	// row was written, for page > 0 only (nil for page 0 itself, or if
	// page 0 hadn't been cached yet when this row was written - Cache.Get
	// falls back to the wall-clock TTL in that case). Lets Cache.Get tell
	// whether a deeper page could have shifted since it was rendered,
	// without an upstream call.
	Page0AnchorID *string
	// CachedAt is when this row was last written.
	CachedAt time.Time
}

// store is the subset of *Store's behavior Cache needs, as an interface so
// tests can inject an in-memory fake (see MemStore) instead of a real
// database. The SQL itself is simple enough to trust to live docker-compose
// verification instead.
type store interface {
	Get(ctx context.Context, showID string, page int) (Row, bool, error)
	Set(ctx context.Context, showID string, page int, r Row) error
	Delete(ctx context.Context, showID string, page int) error
	// DeleteStalePage0 removes every page-0 row cached at or before
	// olderThan, returning how many rows were removed.
	DeleteStalePage0(ctx context.Context, olderThan time.Time) (int64, error)
	Count(ctx context.Context) (int64, error)
}

// Cache is a TTL-based cache of rendered feed pages. Safe for concurrent
// use.
type Cache struct {
	store   store
	ttl     time.Duration
	metrics Observer
}

// New creates a Cache backed by s, whose page-0 entries are considered
// fresh for ttl after being Set. metrics may be nil to skip recording.
func New(s store, ttl time.Duration, metrics Observer) *Cache {
	return &Cache{store: s, ttl: ttl, metrics: metrics}
}

// Get returns the entry cached for showID's page, if present and still
// fresh - see the package doc comment for what "fresh" means for page 0
// versus deeper pages.
func (c *Cache) Get(ctx context.Context, showID string, page int) (Entry, bool) {
	r, ok, err := c.store.Get(ctx, showID, page)
	if err != nil {
		slog.ErrorContext(ctx, "feed cache lookup failed", "error", err, "show_id", showID, "page", page)
		c.observeLookup(ctx, false)
		return Entry{}, false
	}
	if !ok {
		c.observeLookup(ctx, false)
		return Entry{}, false
	}

	fresh := c.fresh(ctx, showID, page, r)
	c.observeLookup(ctx, fresh)
	if !fresh {
		return Entry{}, false
	}
	return r.Entry, true
}

func (c *Cache) fresh(ctx context.Context, showID string, page int, r Row) bool {
	if page == 0 || r.Page0AnchorID == nil {
		return time.Since(r.CachedAt) < c.ttl
	}
	page0, ok, err := c.store.Get(ctx, showID, 0)
	if err != nil {
		slog.ErrorContext(ctx, "feed cache page-0 anchor lookup failed", "error", err, "show_id", showID)
		return false
	}
	if !ok {
		return false
	}
	return page0.FirstDiffusionID == *r.Page0AnchorID
}

// Set stores e as showID's page, fresh from now.
func (c *Cache) Set(ctx context.Context, showID string, page int, e Entry) {
	var firstDiffusionID string
	if len(e.Diffusions) > 0 {
		firstDiffusionID = e.Diffusions[0].ID
	}

	var page0Anchor *string
	if page > 0 {
		if page0, ok, err := c.store.Get(ctx, showID, 0); err == nil && ok {
			id := page0.FirstDiffusionID
			page0Anchor = &id
		}
	}

	r := Row{
		Entry:            e,
		FirstDiffusionID: firstDiffusionID,
		Page0AnchorID:    page0Anchor,
		CachedAt:         time.Now(),
	}
	if err := c.store.Set(ctx, showID, page, r); err != nil {
		slog.ErrorContext(ctx, "feed cache write failed", "error", err, "show_id", showID, "page", page)
	}
}

// Invalidate removes showID's page entry, if any - used once a degraded
// entry's background enrichment has fully caught up (see
// httpapi.EnrichmentStatus.AllResolved), so it gets rebuilt well before its
// TTL would otherwise expire.
func (c *Cache) Invalidate(ctx context.Context, showID string, page int) {
	if err := c.store.Delete(ctx, showID, page); err != nil {
		slog.ErrorContext(ctx, "feed cache invalidate failed", "error", err, "show_id", showID, "page", page)
	}
}

// Sweep periodically evicts page-0 entries past their TTL and refreshes the
// entry-count gauge, until ctx is done. Meant to be started as a background
// goroutine.
func (c *Cache) Sweep(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sweepOnce(ctx)
		}
	}
}

func (c *Cache) sweepOnce(ctx context.Context) {
	if _, err := c.store.DeleteStalePage0(ctx, time.Now().Add(-c.ttl)); err != nil {
		slog.ErrorContext(ctx, "feed cache sweep failed", "error", err)
	}

	if c.metrics == nil {
		return
	}
	count, err := c.store.Count(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "feed cache count failed", "error", err)
		return
	}
	c.metrics.SetFeedCacheEntries(ctx, count)
}

func (c *Cache) observeLookup(ctx context.Context, hit bool) {
	if c.metrics == nil {
		return
	}
	outcome := outcomeMiss
	if hit {
		outcome = outcomeHit
	}
	c.metrics.ObserveFeedCacheLookup(ctx, outcome)
}
