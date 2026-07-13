package episodecache

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sync/singleflight"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// store is the subset of *Store's behavior Resolver needs, as an
// interface so tests can inject an in-memory fake instead of a real
// database. The SQL itself is simple enough to trust to live
// docker-compose verification instead.
type store interface {
	Get(ctx context.Context, manifestationID string) (Entry, bool, error)
	// GetMany returns whichever of manifestationIDs have a cached entry, in
	// one round trip - used by findCachedPrincipal instead of issuing one
	// query per sibling manifestation.
	GetMany(ctx context.Context, manifestationIDs []string) (map[string]Entry, error)
	Upsert(ctx context.Context, e Entry) error
	GetOriginImage(ctx context.Context, diffusionID string) (mainImage string, ok bool, err error)
	UpsertOriginImage(ctx context.Context, diffusionID, mainImage string) error
	GetOriginBody(ctx context.Context, diffusionID string) (bodyMarkdown, standfirst string, createdTime int64, ok bool, err error)
	UpsertOriginBody(ctx context.Context, diffusionID, bodyMarkdown, standfirst string, createdTime int64) error
}

// fetcher is the subset of *radiofrance.Client's behavior Resolver needs.
type fetcher interface {
	GetManifestation(ctx context.Context, manifestationID string) (radiofrance.ManifestationDetails, error)
	GetDiffusionManifestations(ctx context.Context, diffusionID string) (map[string]radiofrance.ManifestationDetails, error)
	GetDiffusion(ctx context.Context, diffusionID string) (radiofrance.Diffusion, error)
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
// API. Implements feed.ManifestationResolver, feed.ImageResolver,
// feed.DescriptionResolver, httpapi.AudioResolver, and
// httpapi.EnrichmentStatus.
//
// Resolve/ResolveImage/ResolveDescription never make an upstream call
// themselves: on a cache miss they enqueue a background job on enricher
// and return a degraded-but-immediate fallback (see each method's doc
// comment), so a request never blocks on Radio France. The actual upstream
// work happens in enrichManifestation/enrichOrigin, run by an Enricher
// worker some time later, which fills in the cache so the *next* request
// for the same episode is a hit. ResolveAudioURL is the one exception -
// see its own doc comment.
type Resolver struct {
	store   store
	fetcher fetcher
	// observer is nil-able; lookups are simply unrecorded if it's nil.
	observer CacheObserver
	enricher *Enricher
	// maxAge bounds how long a cached entry is trusted when it carries no
	// ExpiresAt of its own - see Entry.fresh.
	maxAge time.Duration
	// audioSF dedupes concurrent ResolveAudioURL upstream fetches for the
	// same manifestationID - see its doc comment. Zero value is ready to
	// use, no initialization needed.
	audioSF singleflight.Group
}

// NewResolver creates a Resolver backed by s and f. s is typically a
// *Store; accepting the narrower unexported interface here (rather than
// *Store concretely) is what lets tests inject an in-memory fake without
// exporting that seam. observer may be nil to skip recording cache lookup
// metrics. enricher receives background enrichment jobs on a cache miss -
// see the Resolver doc comment.
func NewResolver(s store, f fetcher, observer CacheObserver, enricher *Enricher, maxAge time.Duration) *Resolver {
	return &Resolver{store: s, fetcher: f, observer: observer, enricher: enricher, maxAge: maxAge}
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

// Resolve returns the manifestation ID, playable URL, duration, and
// expiration to use for d's enclosure/itunes:duration, used while building
// a show's feed. included is whatever manifestation data came back inline
// with the diffusions page (see radiofrance.ShowDiffusions.Manifestations) -
// not exhaustive. url is "" when the principal manifestation isn't yet
// known - enrichManifestation is queued to resolve it in the background,
// and the caller falls back to the legacy /audio/ redirect for this item in
// the meantime (which resolves it live - see ResolveAudioURL). expiresAt is
// nil unless the manifestation's own expiration is known, and lets the
// caller (see feed.Build) work out the earliest point at which a fully-
// resolved feed page needs rebuilding even though nothing in it was ever
// degraded - see feedcache.Entry.EarliestExpiry.
//
// Prefers the manifestation flagged Principal by the API over d's default
// (array position 0): live samples show non-principal manifestations carry
// a real expiration date ~97% of the time, while the principal one never
// does - so this is a correctness concern (dead links), not just cosmetic
// preference. Tries, in order of cost, entirely from data already in hand:
// (1) principal already present in included - free; (2) principal already
// cached from a previous resolution - free. Only on both missing does it
// degrade to d's default manifestation (with no known URL or duration),
// enqueuing background enrichment so a subsequent request resolves it for
// real.
func (r *Resolver) Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) (manifestationID, url string, duration time.Duration, expiresAt *time.Time) {
	candidates := d.Relationships.Manifestations
	if len(candidates) == 0 {
		return "", "", 0, nil
	}

	if id, m, ok := findPrincipal(candidates, included); ok {
		r.cache(ctx, id, d, showID, showTitle, m)
		return id, m.URL, m.Duration, m.ExpiresAt
	}

	if id, e, ok := r.findCachedPrincipal(ctx, candidates, d.UpdatedTime); ok {
		return id, e.URL, e.Duration, e.ExpiresAt
	}

	r.enricher.enqueue(manifestationKey(d.ID), manifestationJob{showID: showID, showTitle: showTitle, d: d, included: included})
	return d.ManifestationID(), "", 0, nil
}

// enrichManifestation resolves and caches d's principal manifestation,
// run by an Enricher worker after Resolve enqueues it on a cache miss.
// Reports whether a principal manifestation was found - see the job
// interface's doc comment for how that's used.
func (r *Resolver) enrichManifestation(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) bool {
	candidates := d.Relationships.Manifestations
	if len(candidates) == 0 {
		return false
	}

	// A concurrent request may have already resolved this since it was
	// enqueued - recheck cheaply before doing any upstream work.
	if id, m, ok := findPrincipal(candidates, included); ok {
		r.cache(ctx, id, d, showID, showTitle, m)
		return true
	}
	if _, _, ok := r.findCachedPrincipal(ctx, candidates, d.UpdatedTime); ok {
		return true
	}

	// One call resolves every sibling manifestation Radio France inlines
	// for this diffusion (coverage isn't guaranteed exhaustive - see
	// GetDiffusionManifestations), instead of up to len(candidates)
	// separate GetManifestation calls.
	fetched, err := r.fetcher.GetDiffusionManifestations(ctx, d.ID)
	if err != nil {
		slog.Warn("episodecache: failed to fetch diffusion manifestations", "diffusionID", d.ID, "error", err)
		fetched = nil
	}

	foundPrincipal := false
	for id, details := range fetched {
		r.cache(ctx, id, d, showID, showTitle, details)
		if details.Principal {
			foundPrincipal = true
		}
	}
	if foundPrincipal {
		return true
	}

	// Whatever's still missing from the bulk response, resolve one at a
	// time, stopping at the first principal found - the same rescue path
	// used before GetDiffusionManifestations existed.
	for _, id := range candidates {
		if _, ok := fetched[id]; ok {
			continue // already resolved (and cached) above
		}
		details, err := r.fetcher.GetManifestation(ctx, id)
		if err != nil {
			slog.Warn("episodecache: failed to fetch a candidate manifestation", "manifestationID", id, "error", err)
			continue
		}
		r.cache(ctx, id, d, showID, showTitle, details)
		if details.Principal {
			return true
		}
	}

	slog.Warn("episodecache: no candidate manifestation could be resolved, feed item will have no duration",
		"diffusionID", d.ID)
	return false
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
// flagged Principal, and still fresh for diffusionUpdatedTime. Fetches all
// of candidates in a single store round trip (see store.GetMany) rather
// than one query per candidate - candidates is typically every sibling
// manifestation of one diffusion (~8), and this runs for every diffusion
// on a page, so batching here avoids len(candidates)*N sequential DB round
// trips per feed build.
func (r *Resolver) findCachedPrincipal(ctx context.Context, candidates []string, diffusionUpdatedTime int64) (string, Entry, bool) {
	cached, err := r.store.GetMany(ctx, candidates)
	if err != nil {
		slog.Warn("episodecache: failed to batch-fetch cached manifestations", "error", err)
		cached = nil
	}
	for _, id := range candidates {
		e, ok := cached[id]
		hit := ok && e.Principal && e.DiffusionUpdatedTime == diffusionUpdatedTime && e.fresh(r.maxAge)
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

// audioResolution is what the singleflight-deduped part of ResolveAudioURL
// produces for one manifestationID.
type audioResolution struct {
	url, showID, showTitle string
}

// ResolveAudioURL returns the playable URL for manifestationID, used by
// the /audio/ redirect. showID/showTitle are whatever was already known
// about this manifestation (populated by a prior Resolve call during a
// feed build) - "" if this manifestation has never been seen by Resolve,
// e.g. an old link from before this cache existed.
//
// Unlike Resolve/ResolveImage/ResolveDescription, this makes a synchronous
// live call on a cache miss rather than enqueuing background enrichment:
// it isn't building a feed with a degradable fallback - a listener clicking
// play needs the real URL right now, there's nothing to degrade to. This is
// a deliberate, permanent exception to "upstream concurrency only happens
// via the enrichment queue".
//
// The upstream fetch and cache write are deduped by audioSF: if several
// requests race on the same cold manifestationID (e.g. a popular episode
// just went stale), only one actually calls GetManifestation, and every
// caller gets its result - rather than each firing its own redundant
// upstream call. A side effect is that on a race, every caller's
// showID/showTitle attribution comes from whichever caller's entry won the
// race; since they all read the same store row, in practice that's the
// same value regardless of which caller wins.
func (r *Resolver) ResolveAudioURL(ctx context.Context, manifestationID string) (url, showID, showTitle string, err error) {
	entry, ok, getErr := r.store.Get(ctx, manifestationID)
	hit := getErr == nil && ok && entry.fresh(r.maxAge)
	r.observeCacheLookup(ctx, hit)
	if hit {
		return entry.URL, entry.ShowID, entry.ShowTitle, nil
	}

	v, err, _ := r.audioSF.Do(manifestationID, func() (any, error) {
		details, err := r.fetcher.GetManifestation(ctx, manifestationID)
		if err != nil {
			return nil, err
		}

		// Preserve whatever show attribution a prior Resolve call already
		// established for this manifestation, rather than clobbering it
		// with empty values just because this URL needed refreshing.
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
		return audioResolution{url: details.URL, showID: entry.ShowID, showTitle: entry.ShowTitle}, nil
	})
	if err != nil {
		return "", "", "", err
	}
	res := v.(audioResolution)
	return res.url, res.showID, res.showTitle, nil
}

// ResolveImage returns the cover image URL to use for d, implementing
// feed.ImageResolver.
//
// d's own MainImage/Visuals are used directly when MainImage is present -
// see radiofrance.DiffusionImageURL. Reruns typically have no MainImage of
// their own, though, and fall back to Visuals, which is usually just the
// enclosing show's shared banner rather than real per-episode art (see
// radiofrance.Diffusion.OriginDiffusionID). For a rerun, this instead
// resolves the origin broadcast's MainImage - cached indefinitely in
// Postgres, since the origin diffusion's editorial content and artwork are
// fixed once the episode has aired, unlike a manifestation's playable URL
// which can expire. On a cache miss, enrichOrigin is queued to resolve it
// in the background and this falls back to DiffusionImageURL(d) for now.
func (r *Resolver) ResolveImage(ctx context.Context, d radiofrance.Diffusion) string {
	if d.MainImage != "" {
		return radiofrance.DiffusionImageURL(d)
	}

	originID := d.OriginDiffusionID()
	if originID == "" {
		return radiofrance.DiffusionImageURL(d)
	}

	if mainImage, ok, err := r.store.GetOriginImage(ctx, originID); err == nil && ok {
		r.observeCacheLookup(ctx, true)
		if mainImage != "" {
			return radiofrance.ImageURL(nil, mainImage)
		}
		return radiofrance.DiffusionImageURL(d)
	} else if err != nil {
		slog.Warn("episodecache: failed to read origin diffusion image cache", "diffusionID", originID, "error", err)
	}
	r.observeCacheLookup(ctx, false)

	r.enricher.enqueue(originKey(originID), originJob{originID: originID})
	return radiofrance.DiffusionImageURL(d)
}

// ResolveDescription returns the (bodyMarkdown, standfirst) pair to use for
// d's feed description, implementing feed.DescriptionResolver.
// originCreatedTime is the origin diffusion's own CreatedTime (its original
// broadcast date) as a Unix timestamp, taken from the cache, or 0 if d
// isn't a rerun or the origin hasn't been resolved yet - callers use it to
// flag a rerun in the feed, independent of whether the origin's
// bodyMarkdown ended up being usable.
//
// Unlike a rerun's MainImage (see ResolveImage), a rerun's own bodyMarkdown
// is rarely blank - but live samples show it's often a flattened,
// auto-derived copy of the origin broadcast's real editorial notes, stripped
// of links, bold, lists, and embed shortcodes (and observed once to even
// swap out a reference the origin had). For a rerun, this prefers the origin
// diffusion's cached bodyMarkdown/standfirst - same rationale as
// ResolveImage - falling back to d's own fields if the origin's turn out to
// be blank or aren't cached yet. On a cache miss, enrichOrigin is queued to
// resolve it in the background (deduped with any job ResolveImage already
// queued for the same origin - see enrichOrigin).
func (r *Resolver) ResolveDescription(ctx context.Context, d radiofrance.Diffusion) (bodyMarkdown, standfirst string, originCreatedTime int64) {
	originID := d.OriginDiffusionID()
	if originID == "" {
		return d.BodyMarkdown, d.Standfirst, 0
	}

	if body, sf, ct, ok, err := r.store.GetOriginBody(ctx, originID); err == nil && ok {
		r.observeCacheLookup(ctx, true)
		if isBlank(body) {
			return d.BodyMarkdown, d.Standfirst, ct
		}
		return body, sf, ct
	} else if err != nil {
		slog.Warn("episodecache: failed to read origin diffusion body cache", "diffusionID", originID, "error", err)
	}
	r.observeCacheLookup(ctx, false)

	r.enricher.enqueue(originKey(originID), originJob{originID: originID})
	return d.BodyMarkdown, d.Standfirst, 0
}

// enrichOrigin resolves and caches originID's MainImage and
// bodyMarkdown/standfirst/createdTime in a single GetDiffusion call, run by
// an Enricher worker after ResolveImage/ResolveDescription enqueue it on a
// cache miss - merging what used to be two separate live-fetch paths into
// one upstream call. Reports whether the origin diffusion was
// fetched successfully.
func (r *Resolver) enrichOrigin(ctx context.Context, originID string) bool {
	// A concurrent request may have already resolved this since it was
	// enqueued - recheck cheaply before doing any upstream work. Only skip
	// entirely once both are cached; if just one is missing, it's still
	// worth the one GetDiffusion call to fill in the rest.
	_, imageCached, imageErr := r.store.GetOriginImage(ctx, originID)
	_, _, _, bodyCached, bodyErr := r.store.GetOriginBody(ctx, originID)
	if imageErr == nil && imageCached && bodyErr == nil && bodyCached {
		return true
	}

	origin, err := r.fetcher.GetDiffusion(ctx, originID)
	if err != nil {
		slog.Warn("episodecache: failed to fetch origin diffusion", "diffusionID", originID, "error", err)
		return false
	}

	if err := r.store.UpsertOriginImage(ctx, originID, origin.MainImage); err != nil {
		slog.Error("episodecache: failed to cache origin diffusion image", "diffusionID", originID, "error", err)
	}
	if err := r.store.UpsertOriginBody(ctx, originID, origin.BodyMarkdown, origin.Standfirst, origin.CreatedTime); err != nil {
		slog.Error("episodecache: failed to cache origin diffusion body", "diffusionID", originID, "error", err)
	}
	return true
}

// AllResolved reports whether every diffusion in diffusions (and, for
// reruns, its origin) has finished background enrichment - implements
// httpapi.EnrichmentStatus (declared there to keep that package decoupled
// from episodecache), letting a degraded cached feed page be invalidated
// as soon as it catches up instead of waiting out the feed cache's TTL.
// A key whose enrichment job recently failed counts as not-yet-resolved
// too, for failureBackoff - otherwise a persistently failing upstream
// would make every single request invalidate and rebuild the page, instead
// of settling into serving the stale degraded copy until the backoff
// elapses. Deliberately cheap: just in-memory lookups (see
// Enricher.isPending/isBackingOff), no DB reads or upstream calls, so it's
// fine to call on every cache hit for a page that was degraded when it was
// cached.
func (r *Resolver) AllResolved(diffusions []radiofrance.Diffusion) bool {
	for _, d := range diffusions {
		if r.enricher.isPending(manifestationKey(d.ID)) || r.enricher.isBackingOff(manifestationKey(d.ID)) {
			return false
		}
		if originID := d.OriginDiffusionID(); originID != "" {
			if r.enricher.isPending(originKey(originID)) || r.enricher.isBackingOff(originKey(originID)) {
				return false
			}
		}
	}
	return true
}

// isBlank reports whether s is empty or contains nothing but
// whitespace/periods - the same definition feed.isPlaceholder uses for a
// meaningless bodyMarkdown/standfirst value. Duplicated here (rather than
// imported) to avoid a dependency from this package back onto feed, which
// already depends on episodecache through the resolver interfaces.
func isBlank(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.'
	}) == ""
}
