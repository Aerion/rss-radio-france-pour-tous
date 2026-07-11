package episodecache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// fakeStore is an in-memory store for testing Resolver's logic without a
// real database.
type fakeStore struct {
	entries map[string]Entry
	gets    int
	upserts int
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[string]Entry{}}
}

func (s *fakeStore) Get(ctx context.Context, manifestationID string) (Entry, bool, error) {
	s.gets++
	e, ok := s.entries[manifestationID]
	return e, ok, nil
}

func (s *fakeStore) Upsert(ctx context.Context, e Entry) error {
	s.upserts++
	if e.FetchedAt.IsZero() {
		e.FetchedAt = time.Now()
	}
	s.entries[e.ManifestationID] = e
	return nil
}

type fakeFetcher struct {
	details map[string]radiofrance.ManifestationDetails
	err     error
	calls   int
}

func (f *fakeFetcher) GetManifestation(ctx context.Context, manifestationID string) (radiofrance.ManifestationDetails, error) {
	f.calls++
	if f.err != nil {
		return radiofrance.ManifestationDetails{}, f.err
	}
	return f.details[manifestationID], nil
}

func diffusionWithManifestation(id, manifestationID string, updatedTime int64) radiofrance.Diffusion {
	d := radiofrance.Diffusion{ID: id, UpdatedTime: updatedTime}
	d.Relationships.Manifestations = []string{manifestationID}
	return d
}

func TestResolve_CacheMissFetchesAndCaches(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/a.mp3", Duration: 90 * time.Second},
	}}
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestation("d1", "m1", 100)
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d)

	if manifestationID != "m1" {
		t.Errorf("manifestationID = %q, want m1", manifestationID)
	}
	if duration != 90*time.Second {
		t.Errorf("duration = %v, want 90s", duration)
	}
	if fetcher.calls != 1 {
		t.Errorf("fetcher.calls = %d, want 1", fetcher.calls)
	}
	if store.upserts != 1 {
		t.Errorf("store.upserts = %d, want 1", store.upserts)
	}
}

func TestResolve_FreshCacheHitSkipsFetch(t *testing.T) {
	store := newFakeStore()
	store.entries["m1"] = Entry{
		ManifestationID: "m1", DiffusionUpdatedTime: 100, Duration: 90 * time.Second, FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{}
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestation("d1", "m1", 100)
	_, duration := r.Resolve(context.Background(), "show1", "Show One", d)

	if duration != 90*time.Second {
		t.Errorf("duration = %v, want 90s (from cache)", duration)
	}
	if fetcher.calls != 0 {
		t.Errorf("fetcher.calls = %d, want 0 (should not re-fetch a fresh entry)", fetcher.calls)
	}
}

func TestResolve_StaleCacheDueToUpdatedTimeChangeRefetches(t *testing.T) {
	store := newFakeStore()
	store.entries["m1"] = Entry{
		ManifestationID: "m1", DiffusionUpdatedTime: 100, Duration: 90 * time.Second, FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/a-v2.mp3", Duration: 120 * time.Second},
	}}
	r := NewResolver(store, fetcher)

	// Diffusion's updatedTime moved on (200 vs cached 100) - episode was edited.
	d := diffusionWithManifestation("d1", "m1", 200)
	_, duration := r.Resolve(context.Background(), "show1", "Show One", d)

	if duration != 120*time.Second {
		t.Errorf("duration = %v, want 120s (refetched)", duration)
	}
	if fetcher.calls != 1 {
		t.Errorf("fetcher.calls = %d, want 1", fetcher.calls)
	}
}

func TestResolve_NoManifestationReturnsEmpty(t *testing.T) {
	r := NewResolver(newFakeStore(), &fakeFetcher{})

	d := radiofrance.Diffusion{ID: "d1"} // no Relationships.Manifestations
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d)

	if manifestationID != "" || duration != 0 {
		t.Errorf("got (%q, %v), want (\"\", 0)", manifestationID, duration)
	}
}

func TestResolve_UpstreamErrorDegradesGracefully(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{err: errors.New("upstream boom")}
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestation("d1", "m1", 100)
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d)

	if manifestationID != "m1" {
		t.Errorf("manifestationID = %q, want m1 (fallback to raw ID)", manifestationID)
	}
	if duration != 0 {
		t.Errorf("duration = %v, want 0 (unknown)", duration)
	}
	if store.upserts != 0 {
		t.Errorf("store.upserts = %d, want 0 (nothing to cache on error)", store.upserts)
	}
}

func TestResolveAudioURL_CacheHit(t *testing.T) {
	store := newFakeStore()
	store.entries["m1"] = Entry{
		ManifestationID: "m1", URL: "https://cdn.example.com/a.mp3",
		ShowID: "show1", ShowTitle: "Show One", FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{}
	r := NewResolver(store, fetcher)

	url, showID, showTitle, err := r.ResolveAudioURL(context.Background(), "m1")
	if err != nil {
		t.Fatalf("ResolveAudioURL: %v", err)
	}
	if url != "https://cdn.example.com/a.mp3" || showID != "show1" || showTitle != "Show One" {
		t.Errorf("got (%q, %q, %q)", url, showID, showTitle)
	}
	if fetcher.calls != 0 {
		t.Errorf("fetcher.calls = %d, want 0", fetcher.calls)
	}
}

func TestResolveAudioURL_CacheMissPreservesNoPriorShowInfo(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/a.mp3"},
	}}
	r := NewResolver(store, fetcher)

	url, showID, showTitle, err := r.ResolveAudioURL(context.Background(), "m1")
	if err != nil {
		t.Fatalf("ResolveAudioURL: %v", err)
	}
	if url != "https://cdn.example.com/a.mp3" {
		t.Errorf("url = %q", url)
	}
	if showID != "" || showTitle != "" {
		t.Errorf("got showID=%q showTitle=%q, want both empty (never seen by Resolve)", showID, showTitle)
	}
}

func TestResolveAudioURL_ExpiredEntryRefetchesAndKeepsShowInfo(t *testing.T) {
	store := newFakeStore()
	expired := time.Now().Add(-time.Hour)
	store.entries["m1"] = Entry{
		ManifestationID: "m1", URL: "https://cdn.example.com/old.mp3",
		ShowID: "show1", ShowTitle: "Show One", ExpiresAt: &expired, FetchedAt: time.Now().Add(-48 * time.Hour),
	}
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/fresh.mp3"},
	}}
	r := NewResolver(store, fetcher)

	url, showID, showTitle, err := r.ResolveAudioURL(context.Background(), "m1")
	if err != nil {
		t.Fatalf("ResolveAudioURL: %v", err)
	}
	if url != "https://cdn.example.com/fresh.mp3" {
		t.Errorf("url = %q, want the refreshed URL", url)
	}
	if showID != "show1" || showTitle != "Show One" {
		t.Errorf("got showID=%q showTitle=%q, want preserved from before the refresh", showID, showTitle)
	}
	if fetcher.calls != 1 {
		t.Errorf("fetcher.calls = %d, want 1", fetcher.calls)
	}
}

func TestResolveAudioURL_UpstreamErrorPropagates(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{err: errors.New("upstream boom")}
	r := NewResolver(store, fetcher)

	_, _, _, err := r.ResolveAudioURL(context.Background(), "m1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}
