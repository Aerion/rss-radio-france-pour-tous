package httpapi

import (
	"context"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// fakeAPI is a test double for API, avoiding any real HTTP round-trip to
// Radio France - mirroring the mocking boundary the original Vitest suite
// used (stubbing the global fetch).
type fakeAPI struct {
	showDiffusions      radiofrance.ShowDiffusions
	showDiffusionsErr   error
	showDiffusionsCalls int

	searchResults []radiofrance.SearchResult
	searchErr     error
}

func (f *fakeAPI) GetShowDiffusions(ctx context.Context, showID string, page int) (radiofrance.ShowDiffusions, error) {
	f.showDiffusionsCalls++
	return f.showDiffusions, f.showDiffusionsErr
}

func (f *fakeAPI) Search(ctx context.Context, query string) ([]radiofrance.SearchResult, error) {
	return f.searchResults, f.searchErr
}

// fakeAudioResolver is a test double for AudioResolver.
type fakeAudioResolver struct {
	url       string
	showID    string
	showTitle string
	err       error
}

func (f *fakeAudioResolver) ResolveAudioURL(ctx context.Context, manifestationID string) (url, showID, showTitle string, err error) {
	return f.url, f.showID, f.showTitle, f.err
}

// fakeEnrichmentStatus is a test double for EnrichmentStatus. allResolved
// is returned as-is by AllResolved; calls counts how many times it was
// invoked, letting tests assert whether the active-invalidation check ran
// at all (it shouldn't, for a cache entry that wasn't degraded).
type fakeEnrichmentStatus struct {
	allResolved bool
	calls       int
}

func (f *fakeEnrichmentStatus) AllResolved(diffusions []radiofrance.Diffusion) bool {
	f.calls++
	return f.allResolved
}

// fakeManifestationResolver is a stand-in for
// internal/episodecache.Resolver's Resolve method, letting tests produce a
// feed with a real (non-degraded) duration/URL without a real cache/API.
type fakeManifestationResolver struct {
	url       string
	duration  time.Duration
	expiresAt *time.Time
}

func (f fakeManifestationResolver) Resolve(ctx context.Context, showID, showTitle string, d radiofrance.Diffusion, included map[string]radiofrance.ManifestationDetails) (string, string, time.Duration, *time.Time) {
	return d.ManifestationID(), f.url, f.duration, f.expiresAt
}
