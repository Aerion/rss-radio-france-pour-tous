// Package feedcache is an in-memory, TTL-based cache of fully-rendered
// feed pages, keyed by show ID and page number. It exists so a request for
// a show whose episode cache is already warm never re-renders the feed
// from scratch, and - together with internal/episodecache's enrichment
// queue - so a request for a *cold* show never blocks on a Radio France
// call: the feed is built once with whatever's already known, cached, and
// served as-is (possibly with some items still missing duration/artwork)
// until either its TTL elapses or background enrichment catches up (see
// Entry.HadDegraded and the active-invalidation check in internal/httpapi).
//
// Deliberately in-memory rather than Postgres-backed: this is a
// memoization of local rendering work, not of upstream network calls, so
// losing it on a restart just costs re-rendering from already-fast cache
// reads, not a fresh Radio France round-trip. See the design doc for the
// full rationale.
package feedcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// Observer receives the outcome of each feed cache lookup and changes to
// the current entry count, for cache-effectiveness monitoring. Defined
// here rather than in a metrics package so this package stays decoupled
// from any particular metrics backend; observability.Observability
// implements it.
type Observer interface {
	ObserveFeedCacheLookup(ctx context.Context, outcome string)
	AdjustFeedCacheEntries(ctx context.Context, delta int64)
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
}

type entry struct {
	Entry
	cachedAt time.Time
}

// Cache is an in-memory, TTL-based cache of rendered feed pages. Safe for
// concurrent use.
type Cache struct {
	mu      sync.Mutex
	entries map[string]entry
	ttl     time.Duration
	metrics Observer
}

// New creates a Cache whose entries are considered fresh for ttl after
// being Set. metrics may be nil to skip recording.
func New(ttl time.Duration, metrics Observer) *Cache {
	return &Cache{entries: map[string]entry{}, ttl: ttl, metrics: metrics}
}

// Key returns the cache key for a show's page, for use with
// Get/Set/Invalidate.
func Key(showID string, page int) string {
	return fmt.Sprintf("%s/%d", showID, page)
}

// Get returns the entry cached under key, if present and not yet past its
// TTL.
func (c *Cache) Get(ctx context.Context, key string) (Entry, bool) {
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()

	hit := ok && time.Since(e.cachedAt) < c.ttl
	c.observeLookup(ctx, hit)
	if !hit {
		return Entry{}, false
	}
	return e.Entry, true
}

// Set stores e under key, fresh from now.
func (c *Cache) Set(key string, e Entry) {
	c.mu.Lock()
	_, existed := c.entries[key]
	c.entries[key] = entry{Entry: e, cachedAt: time.Now()}
	c.mu.Unlock()
	if !existed {
		c.adjustEntries(1)
	}
}

// Invalidate removes key's entry, if any - used once a degraded entry's
// background enrichment has fully caught up (see
// httpapi.EnrichmentStatus.AllResolved), so it gets rebuilt well before
// its TTL would otherwise expire.
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	_, existed := c.entries[key]
	delete(c.entries, key)
	c.mu.Unlock()
	if existed {
		c.adjustEntries(-1)
	}
}

// Sweep periodically evicts entries past their TTL, until ctx is done.
// Meant to be started as a background goroutine; keeps the entry-count
// gauge accurate for cache entries nothing ever looks up again (e.g. a
// show whose feed is no longer being polled).
func (c *Cache) Sweep(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sweepOnce()
		}
	}
}

func (c *Cache) sweepOnce() {
	now := time.Now()
	c.mu.Lock()
	removed := 0
	for key, e := range c.entries {
		if now.Sub(e.cachedAt) >= c.ttl {
			delete(c.entries, key)
			removed++
		}
	}
	c.mu.Unlock()
	if removed > 0 {
		c.adjustEntries(-int64(removed))
	}
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

func (c *Cache) adjustEntries(delta int64) {
	if c.metrics != nil {
		c.metrics.AdjustFeedCacheEntries(context.Background(), delta)
	}
}
