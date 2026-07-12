package episodecache

import (
	"context"
	"log/slog"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// store is the subset of *Store's behavior Resolver needs, as an
// interface so tests can inject an in-memory fake instead of a real
// database. The SQL itself is simple enough to trust to live
// docker-compose verification instead.
type store interface {
	Get(ctx context.Context, manifestationID string) (Entry, bool, error)
	Upsert(ctx context.Context, e Entry) error
}

// fetcher is the subset of *radiofrance.Client's behavior Resolver needs.
type fetcher interface {
	GetManifestation(ctx context.Context, manifestationID string) (radiofrance.ManifestationDetails, error)
}

// CacheObserver receives the outcome of each manifestation cache lookup,
// for cache-effectiveness monitoring. Defined here rather than in a
// metrics package so this package stays decoupled from any particular
// metrics backend; observability.Observability implements it.
type CacheObserver interface {
	ObserveCacheLookup(ctx context.Context, outcome string)
}

const (
	outcomeHit  = "hit"
	outcomeMiss = "miss"
)

// Resolver turns a diffusion (or a bare manifestation ID) into playback
// details, consulting the cache before falling back to the Radio France
// API. Implements both feed.ManifestationResolver and
// httpapi.AudioResolver.
type Resolver struct {
	store   store
	fetcher fetcher
	// observer is nil-able; lookups are simply unrecorded if it's nil.
	observer CacheObserver
}

// NewResolver creates a Resolver backed by s and f. s is typically a
// *Store; accepting the narrower unexported interface here (rather than
// *Store concretely) is what lets tests inject an in-memory fake without
// exporting that seam. observer may be nil to skip recording cache
// lookup metrics.
func NewResolver(s store, f fetcher, observer CacheObserver) *Resolver {
	return &Resolver{store: s, fetcher: f, observer: observer}
}

// observeCacheLookup records whether a single store.Get call yielded a
// usable entry.
func (r *Resolver) observeCacheLookup(ctx context.Context, hit bool) {
	if r.observer == nil {
		return
	}
	outcome := outcomeMiss
	if hit {
		outcome = outcomeHit
	}
	r.observer.ObserveCacheLookup(ctx, outcome)
}

// Resolve returns the manifestation ID, playable URL, and duration to use
// for d's enclosure/itunes:duration, used while building a show's feed.
// included is whatever manifestation data came back inline with the
// diffusions page (see radiofrance.ShowDiffusions.Manifestations) - not
// exhaustive. url is "" only when every candidate manifestation failed to
// resolve, in which case the caller falls back to the legacy /audio/
// redirect for this item.
//
// Prefers the manifestation flagged Principal by the API over d's default
// (array position 0): live samples show non-principal manifestations carry
// a real expiration date ~97% of the time, while the principal one never
// does - so this is a correctness concern (dead links), not just cosmetic
// preference. Tries, in order of cost: (1) principal already present in
// included - free; (2) principal already cached from a previous
// resolution - free; (3) live-fetch whichever siblings aren't already
// known, stopping at the first principal found. Only degrades to d's
// default manifestation (with no known URL or duration) if every candidate
// failed to fetch, which should be rare.
//
// Never returns an error to the caller: any upstream failure is logged and
// degrades gracefully, so one bad episode never fails the whole feed.
func (r *Resolver) Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) (manifestationID, url string, duration time.Duration) {
	candidates := d.Relationships.Manifestations
	if len(candidates) == 0 {
		return "", "", 0
	}

	if id, m, ok := findPrincipal(candidates, included); ok {
		r.cache(ctx, id, d, showID, showTitle, m)
		return id, m.URL, m.Duration
	}

	if id, e, ok := r.findCachedPrincipal(ctx, candidates, d.UpdatedTime); ok {
		return id, e.URL, e.Duration
	}

	fallbackID, fallbackURL, fallbackDuration := "", "", time.Duration(0)
	for _, id := range candidates {
		if _, known := included[id]; known {
			continue // already checked above, wasn't principal
		}
		details, err := r.fetcher.GetManifestation(ctx, id)
		if err != nil {
			slog.Warn("episodecache: failed to fetch a candidate manifestation", "manifestationID", id, "error", err)
			continue
		}
		r.cache(ctx, id, d, showID, showTitle, details)
		if details.Principal {
			return id, details.URL, details.Duration
		}
		if fallbackID == "" {
			fallbackID, fallbackURL, fallbackDuration = id, details.URL, details.Duration
		}
	}
	if fallbackID != "" {
		return fallbackID, fallbackURL, fallbackDuration
	}

	slog.Warn("episodecache: no candidate manifestation could be resolved, feed item will have no duration",
		"diffusionID", d.ID)
	return d.ManifestationID(), "", 0
}

// findPrincipal returns the first of candidates flagged Principal in
// included, if any.
func findPrincipal(candidates []string, included map[string]radiofrance.ManifestationDetails) (string, radiofrance.ManifestationDetails, bool) {
	for _, id := range candidates {
		if m, ok := included[id]; ok && m.Principal {
			return id, m, true
		}
	}
	return "", radiofrance.ManifestationDetails{}, false
}

// findCachedPrincipal returns the first of candidates that's cached,
// flagged Principal, and still fresh for diffusionUpdatedTime.
func (r *Resolver) findCachedPrincipal(ctx context.Context, candidates []string, diffusionUpdatedTime int64) (string, Entry, bool) {
	for _, id := range candidates {
		e, ok, err := r.store.Get(ctx, id)
		hit := err == nil && ok && e.Principal && e.DiffusionUpdatedTime == diffusionUpdatedTime && e.fresh()
		r.observeCacheLookup(ctx, hit)
		if hit {
			return id, e, true
		}
	}
	return "", Entry{}, false
}

func (r *Resolver) cache(ctx context.Context, id string, d radiofrance.Diffusion, showID, showTitle string, m radiofrance.ManifestationDetails) {
	entry := Entry{
		ManifestationID:      id,
		DiffusionID:          d.ID,
		ShowID:               showID,
		ShowTitle:            showTitle,
		URL:                  m.URL,
		Duration:             m.Duration,
		Principal:            m.Principal,
		DiffusionUpdatedTime: d.UpdatedTime,
		ExpiresAt:            m.ExpiresAt,
	}
	if err := r.store.Upsert(ctx, entry); err != nil {
		slog.Error("episodecache: failed to cache manifestation", "manifestationID", id, "error", err)
	}
}

// ResolveAudioURL returns the playable URL for manifestationID, used by
// the /audio/ redirect. showID/showTitle are whatever was already known
// about this manifestation (populated by a prior Resolve call during a
// feed build) - "" if this manifestation has never been seen by Resolve,
// e.g. an old link from before this cache existed.
func (r *Resolver) ResolveAudioURL(ctx context.Context, manifestationID string) (url, showID, showTitle string, err error) {
	entry, ok, getErr := r.store.Get(ctx, manifestationID)
	hit := getErr == nil && ok && entry.fresh()
	r.observeCacheLookup(ctx, hit)
	if hit {
		return entry.URL, entry.ShowID, entry.ShowTitle, nil
	}

	details, err := r.fetcher.GetManifestation(ctx, manifestationID)
	if err != nil {
		return "", "", "", err
	}

	// Preserve whatever show attribution a prior Resolve call already
	// established for this manifestation, rather than clobbering it with
	// empty values just because this URL needed refreshing.
	updated := Entry{
		ManifestationID:      manifestationID,
		DiffusionID:          entry.DiffusionID,
		ShowID:               entry.ShowID,
		ShowTitle:            entry.ShowTitle,
		URL:                  details.URL,
		Duration:             details.Duration,
		Principal:            details.Principal,
		DiffusionUpdatedTime: entry.DiffusionUpdatedTime,
		ExpiresAt:            details.ExpiresAt,
	}
	if err := r.store.Upsert(ctx, updated); err != nil {
		slog.Error("episodecache: failed to cache manifestation", "manifestationID", manifestationID, "error", err)
	}
	return details.URL, entry.ShowID, entry.ShowTitle, nil
}
