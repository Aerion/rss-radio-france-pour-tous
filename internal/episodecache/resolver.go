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

// Resolver turns a diffusion (or a bare manifestation ID) into playback
// details, consulting the cache before falling back to the Radio France
// API. Implements both feed.ManifestationResolver and
// httpapi.AudioResolver.
type Resolver struct {
	store   store
	fetcher fetcher
}

// NewResolver creates a Resolver backed by s and f. s is typically a
// *Store; accepting the narrower unexported interface here (rather than
// *Store concretely) is what lets tests inject an in-memory fake without
// exporting that seam.
func NewResolver(s store, f fetcher) *Resolver {
	return &Resolver{store: s, fetcher: f}
}

// Resolve returns the manifestation ID and duration to use for d's
// enclosure/itunes:duration, used while building a show's feed. It never
// returns an error to the caller: on any upstream failure it logs and
// degrades to (d.ManifestationID(), 0), so one bad episode never fails the
// whole feed.
func (r *Resolver) Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion) (manifestationID string, duration time.Duration) {
	manifestationID = d.ManifestationID()
	if manifestationID == "" {
		return "", 0
	}

	if entry, ok, err := r.store.Get(ctx, manifestationID); err == nil && ok &&
		entry.DiffusionUpdatedTime == d.UpdatedTime && entry.fresh() {
		return manifestationID, entry.Duration
	}

	details, err := r.fetcher.GetManifestation(ctx, manifestationID)
	if err != nil {
		slog.Warn("episodecache: failed to resolve manifestation, feed item will have no duration",
			"manifestationID", manifestationID, "error", err)
		return manifestationID, 0
	}

	entry := Entry{
		ManifestationID:      manifestationID,
		DiffusionID:          d.ID,
		ShowID:               showID,
		ShowTitle:            showTitle,
		URL:                  details.URL,
		Duration:             details.Duration,
		Principal:            details.Principal,
		DiffusionUpdatedTime: d.UpdatedTime,
		ExpiresAt:            details.ExpiresAt,
	}
	if err := r.store.Upsert(ctx, entry); err != nil {
		slog.Error("episodecache: failed to cache manifestation", "manifestationID", manifestationID, "error", err)
	}
	return manifestationID, details.Duration
}

// ResolveAudioURL returns the playable URL for manifestationID, used by
// the /audio/ redirect. showID/showTitle are whatever was already known
// about this manifestation (populated by a prior Resolve call during a
// feed build) - "" if this manifestation has never been seen by Resolve,
// e.g. an old link from before this cache existed.
func (r *Resolver) ResolveAudioURL(ctx context.Context, manifestationID string) (url, showID, showTitle string, err error) {
	entry, ok, getErr := r.store.Get(ctx, manifestationID)
	if getErr == nil && ok && entry.fresh() {
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
