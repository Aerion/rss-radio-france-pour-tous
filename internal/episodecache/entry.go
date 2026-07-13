// Package episodecache caches Radio France manifestation details
// (playable URL, duration, expiration) in Postgres, keyed by manifestation
// ID, to cut down on redundant upstream calls.
package episodecache

import "time"

// Entry is one cached manifestation's details.
type Entry struct {
	ManifestationID string
	DiffusionID     string
	// ShowID/ShowTitle are "" when this entry was populated from a direct
	// /audio/ hit with no prior feed-build context (see Resolver.ResolveAudioURL).
	ShowID    string
	ShowTitle string
	URL       string
	Duration  time.Duration
	Principal bool
	// DiffusionUpdatedTime is the diffusion's updatedTime at the moment
	// this entry was fetched, used to detect a since-edited episode.
	DiffusionUpdatedTime int64
	// ExpiresAt is when URL may stop working, or nil if unknown.
	ExpiresAt *time.Time
	FetchedAt time.Time
}

// fresh reports whether e can be used without re-fetching. maxAge bounds
// how long e is trusted when ExpiresAt is unset - Radio France doesn't
// always report an expiration, so this is the fallback safety net against
// serving an indefinitely stale URL (see config.Config.ManifestationCacheMaxAge).
func (e Entry) fresh(maxAge time.Duration) bool {
	if e.ExpiresAt != nil {
		return e.ExpiresAt.After(time.Now())
	}
	return time.Since(e.FetchedAt) < maxAge
}
