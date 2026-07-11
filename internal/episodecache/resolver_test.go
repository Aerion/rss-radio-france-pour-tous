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

// fakeFetcher is a test double for fetcher. err applies to every ID unless
// errByID has a specific entry for it, letting tests simulate "this one
// sibling fails, the next succeeds".
type fakeFetcher struct {
	details map[string]radiofrance.ManifestationDetails
	err     error
	errByID map[string]error
	calls   int
	callIDs []string
}

func (f *fakeFetcher) GetManifestation(ctx context.Context, manifestationID string) (radiofrance.ManifestationDetails, error) {
	f.calls++
	f.callIDs = append(f.callIDs, manifestationID)
	if f.errByID != nil {
		if err, ok := f.errByID[manifestationID]; ok {
			return radiofrance.ManifestationDetails{}, err
		}
	}
	if f.err != nil {
		return radiofrance.ManifestationDetails{}, f.err
	}
	return f.details[manifestationID], nil
}

func diffusionWithManifestations(id string, updatedTime int64, manifestationIDs ...string) radiofrance.Diffusion {
	d := radiofrance.Diffusion{ID: id, UpdatedTime: updatedTime}
	d.Relationships.Manifestations = manifestationIDs
	return d
}

func diffusionWithManifestation(id, manifestationID string, updatedTime int64) radiofrance.Diffusion {
	return diffusionWithManifestations(id, updatedTime, manifestationID)
}

func TestResolve_PrefersPrincipalFromIncluded(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{} // should not be called at all
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	included := map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Duration: 90 * time.Second, Principal: false},
		"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 91 * time.Second, Principal: true},
		"m3": {URL: "https://cdn.example.com/m3.mp3", Duration: 92 * time.Second, Principal: false},
	}

	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d, included)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (the principal one)", manifestationID)
	}
	if duration != 91*time.Second {
		t.Errorf("duration = %v, want 91s", duration)
	}
	if fetcher.calls != 0 {
		t.Errorf("fetcher.calls = %d, want 0 (principal was already in included data)", fetcher.calls)
	}
	if store.upserts != 1 {
		t.Errorf("store.upserts = %d, want 1", store.upserts)
	}
	if !store.entries["m2"].Principal {
		t.Error("expected the cached entry to be flagged Principal")
	}
}

func TestResolve_FallsBackToCachedPrincipalWhenNotInIncluded(t *testing.T) {
	store := newFakeStore()
	store.entries["m2"] = Entry{
		ManifestationID: "m2", Principal: true, DiffusionUpdatedTime: 100,
		Duration: 91 * time.Second, FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{}
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	// included has data, but none of it is principal (and m2 - the actual
	// cached principal - isn't in included at all this time).
	included := map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Principal: false},
		"m3": {URL: "https://cdn.example.com/m3.mp3", Principal: false},
	}

	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d, included)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (cached principal)", manifestationID)
	}
	if duration != 91*time.Second {
		t.Errorf("duration = %v, want 91s", duration)
	}
	if fetcher.calls != 0 {
		t.Errorf("fetcher.calls = %d, want 0 (found via cache, no live fetch needed)", fetcher.calls)
	}
}

func TestResolve_FallsBackToLiveFetchStoppingAtFirstPrincipal(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Duration: 10 * time.Second, Principal: false},
		"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 20 * time.Second, Principal: true},
		"m3": {URL: "https://cdn.example.com/m3.mp3", Duration: 30 * time.Second, Principal: false},
	}}
	r := NewResolver(store, fetcher)

	// Nothing in included and nothing cached - must fetch live.
	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (the principal one)", manifestationID)
	}
	if duration != 20*time.Second {
		t.Errorf("duration = %v, want 20s", duration)
	}
	if fetcher.calls != 2 {
		t.Errorf("fetcher.calls = %d, want 2 (stops as soon as principal m2 is found, never tries m3)", fetcher.calls)
	}
	// Both m1 (non-principal, tried first) and m2 (principal, found) should
	// have been cached along the way.
	if store.upserts != 2 {
		t.Errorf("store.upserts = %d, want 2", store.upserts)
	}
}

func TestResolve_DegradesToFirstSuccessfulFetchWhenNoPrincipalFound(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Duration: 10 * time.Second, Principal: false},
		"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 20 * time.Second, Principal: false},
	}}
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m1" {
		t.Errorf("manifestationID = %q, want m1 (first successful fetch, none were principal)", manifestationID)
	}
	if duration != 10*time.Second {
		t.Errorf("duration = %v, want 10s", duration)
	}
	if fetcher.calls != 2 {
		t.Errorf("fetcher.calls = %d, want 2 (tried every candidate looking for principal)", fetcher.calls)
	}
}

func TestResolve_DegradesToDefaultManifestationWhenEveryFetchFails(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{err: errors.New("upstream boom")}
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m1" {
		t.Errorf("manifestationID = %q, want m1 (d.ManifestationID() fallback)", manifestationID)
	}
	if duration != 0 {
		t.Errorf("duration = %v, want 0 (unknown)", duration)
	}
	if store.upserts != 0 {
		t.Errorf("store.upserts = %d, want 0 (nothing to cache, everything failed)", store.upserts)
	}
}

func TestResolve_SkipsFailingCandidateAndTriesNext(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{
		errByID: map[string]error{"m1": errors.New("gone")},
		details: map[string]radiofrance.ManifestationDetails{
			"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 20 * time.Second, Principal: true},
		},
	}
	r := NewResolver(store, fetcher)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (m1 failed, m2 succeeded and is principal)", manifestationID)
	}
	if duration != 20*time.Second {
		t.Errorf("duration = %v, want 20s", duration)
	}
}

func TestResolve_NoManifestationReturnsEmpty(t *testing.T) {
	r := NewResolver(newFakeStore(), &fakeFetcher{})

	d := radiofrance.Diffusion{ID: "d1"} // no Relationships.Manifestations
	manifestationID, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "" || duration != 0 {
		t.Errorf("got (%q, %v), want (\"\", 0)", manifestationID, duration)
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
