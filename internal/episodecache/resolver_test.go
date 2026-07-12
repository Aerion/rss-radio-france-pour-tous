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
	entries       map[string]Entry
	gets          int
	upserts       int
	originImages  map[string]string
	originGets    int
	originUpserts int
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[string]Entry{}, originImages: map[string]string{}}
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

func (s *fakeStore) GetOriginImage(ctx context.Context, diffusionID string) (string, bool, error) {
	s.originGets++
	mainImage, ok := s.originImages[diffusionID]
	return mainImage, ok, nil
}

func (s *fakeStore) UpsertOriginImage(ctx context.Context, diffusionID, mainImage string) error {
	s.originUpserts++
	s.originImages[diffusionID] = mainImage
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

	diffusions     map[string]radiofrance.Diffusion
	diffusionErr   error
	diffusionCalls int
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

func (f *fakeFetcher) GetDiffusion(ctx context.Context, diffusionID string) (radiofrance.Diffusion, error) {
	f.diffusionCalls++
	if f.diffusionErr != nil {
		return radiofrance.Diffusion{}, f.diffusionErr
	}
	return f.diffusions[diffusionID], nil
}

// fakeCacheObserver records every outcome passed to ObserveCacheLookup, in
// order, for tests to assert against.
type fakeCacheObserver struct {
	outcomes []string
}

func (o *fakeCacheObserver) ObserveCacheLookup(ctx context.Context, outcome string) {
	o.outcomes = append(o.outcomes, outcome)
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
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	included := map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Duration: 90 * time.Second, Principal: false},
		"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 91 * time.Second, Principal: true},
		"m3": {URL: "https://cdn.example.com/m3.mp3", Duration: 92 * time.Second, Principal: false},
	}

	manifestationID, url, duration := r.Resolve(context.Background(), "show1", "Show One", d, included)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (the principal one)", manifestationID)
	}
	if url != "https://cdn.example.com/m2.mp3" {
		t.Errorf("url = %q, want m2's URL", url)
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
		URL: "https://cdn.example.com/m2-cached.mp3", Duration: 91 * time.Second, FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{}
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	// included has data, but none of it is principal (and m2 - the actual
	// cached principal - isn't in included at all this time).
	included := map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Principal: false},
		"m3": {URL: "https://cdn.example.com/m3.mp3", Principal: false},
	}

	manifestationID, url, duration := r.Resolve(context.Background(), "show1", "Show One", d, included)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (cached principal)", manifestationID)
	}
	if url != "https://cdn.example.com/m2-cached.mp3" {
		t.Errorf("url = %q, want the cached m2 URL", url)
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
	r := NewResolver(store, fetcher, nil)

	// Nothing in included and nothing cached - must fetch live.
	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	manifestationID, url, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (the principal one)", manifestationID)
	}
	if url != "https://cdn.example.com/m2.mp3" {
		t.Errorf("url = %q, want m2's URL", url)
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
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	manifestationID, url, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m1" {
		t.Errorf("manifestationID = %q, want m1 (first successful fetch, none were principal)", manifestationID)
	}
	if url != "https://cdn.example.com/m1.mp3" {
		t.Errorf("url = %q, want m1's URL", url)
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
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	manifestationID, url, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m1" {
		t.Errorf("manifestationID = %q, want m1 (d.ManifestationID() fallback)", manifestationID)
	}
	if url != "" {
		t.Errorf("url = %q, want \"\" (nothing resolved, caller falls back to /audio/)", url)
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
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	manifestationID, url, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (m1 failed, m2 succeeded and is principal)", manifestationID)
	}
	if url != "https://cdn.example.com/m2.mp3" {
		t.Errorf("url = %q, want m2's URL", url)
	}
	if duration != 20*time.Second {
		t.Errorf("duration = %v, want 20s", duration)
	}
}

func TestResolve_NoManifestationReturnsEmpty(t *testing.T) {
	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil)

	d := radiofrance.Diffusion{ID: "d1"} // no Relationships.Manifestations
	manifestationID, url, duration := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "" || url != "" || duration != 0 {
		t.Errorf("got (%q, %q, %v), want (\"\", \"\", 0)", manifestationID, url, duration)
	}
}

func TestResolveAudioURL_CacheHit(t *testing.T) {
	store := newFakeStore()
	store.entries["m1"] = Entry{
		ManifestationID: "m1", URL: "https://cdn.example.com/a.mp3",
		ShowID: "show1", ShowTitle: "Show One", FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{}
	r := NewResolver(store, fetcher, nil)

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
	r := NewResolver(store, fetcher, nil)

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
	r := NewResolver(store, fetcher, nil)

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
	r := NewResolver(store, fetcher, nil)

	_, _, _, err := r.ResolveAudioURL(context.Background(), "m1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestResolveAudioURL_ObservesCacheHitAndMiss(t *testing.T) {
	store := newFakeStore()
	store.entries["cached"] = Entry{
		ManifestationID: "cached", URL: "https://cdn.example.com/cached.mp3", FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"uncached": {URL: "https://cdn.example.com/uncached.mp3"},
	}}
	observer := &fakeCacheObserver{}
	r := NewResolver(store, fetcher, observer)

	if _, _, _, err := r.ResolveAudioURL(context.Background(), "cached"); err != nil {
		t.Fatalf("ResolveAudioURL(cached): %v", err)
	}
	if _, _, _, err := r.ResolveAudioURL(context.Background(), "uncached"); err != nil {
		t.Fatalf("ResolveAudioURL(uncached): %v", err)
	}

	want := []string{outcomeHit, outcomeMiss}
	if len(observer.outcomes) != len(want) || observer.outcomes[0] != want[0] || observer.outcomes[1] != want[1] {
		t.Errorf("observer.outcomes = %v, want %v", observer.outcomes, want)
	}
}

func diffusionWithOrigin(id, originID string, visuals ...radiofrance.Visual) radiofrance.Diffusion {
	d := radiofrance.Diffusion{ID: id, Visuals: visuals}
	if originID != "" {
		d.Relationships.OriginDiffusion = []string{originID}
	}
	return d
}

func TestResolveImage_UsesMainImageDirectlyWhenPresent(t *testing.T) {
	fetcher := &fakeFetcher{} // should not be called at all
	r := NewResolver(newFakeStore(), fetcher, nil)

	d := radiofrance.Diffusion{ID: "d1", MainImage: "uuid-episode"}
	got := r.ResolveImage(context.Background(), d)

	want := radiofrance.DiffusionImageURL(d)
	if got != want {
		t.Errorf("ResolveImage = %q, want %q", got, want)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (MainImage already present)", fetcher.diffusionCalls)
	}
}

func TestResolveImage_NoOriginFallsBackToVisuals(t *testing.T) {
	fetcher := &fakeFetcher{}
	r := NewResolver(newFakeStore(), fetcher, nil)

	d := diffusionWithOrigin("d1", "", radiofrance.Visual{Name: "square_banner", VisualUUID: "uuid-banner"})
	got := r.ResolveImage(context.Background(), d)

	want := radiofrance.DiffusionImageURL(d)
	if got != want {
		t.Errorf("ResolveImage = %q, want %q", got, want)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (not a rerun)", fetcher.diffusionCalls)
	}
}

func TestResolveImage_RerunFetchesOriginMainImageOnCacheMiss(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{diffusions: map[string]radiofrance.Diffusion{
		"origin1": {ID: "origin1", MainImage: "uuid-episode"},
	}}
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithOrigin("d1", "origin1", radiofrance.Visual{Name: "square_banner", VisualUUID: "uuid-show-banner"})
	got := r.ResolveImage(context.Background(), d)

	want := radiofrance.ImageURL(nil, "uuid-episode")
	if got != want {
		t.Errorf("ResolveImage = %q, want %q (origin diffusion's MainImage)", got, want)
	}
	if fetcher.diffusionCalls != 1 {
		t.Errorf("fetcher.diffusionCalls = %d, want 1", fetcher.diffusionCalls)
	}
	if store.originImages["origin1"] != "uuid-episode" {
		t.Errorf("store.originImages[origin1] = %q, want it cached", store.originImages["origin1"])
	}
}

func TestResolveImage_RerunUsesCachedOriginImageWithoutFetching(t *testing.T) {
	store := newFakeStore()
	store.originImages["origin1"] = "uuid-episode-cached"
	fetcher := &fakeFetcher{} // should not be called
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithOrigin("d1", "origin1")
	got := r.ResolveImage(context.Background(), d)

	want := radiofrance.ImageURL(nil, "uuid-episode-cached")
	if got != want {
		t.Errorf("ResolveImage = %q, want %q", got, want)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (cache hit)", fetcher.diffusionCalls)
	}
}

func TestResolveImage_RerunFallsBackToVisualsWhenOriginHasNoMainImage(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{diffusions: map[string]radiofrance.Diffusion{
		"origin1": {ID: "origin1"}, // no MainImage either
	}}
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithOrigin("d1", "origin1", radiofrance.Visual{Name: "square_banner", VisualUUID: "uuid-show-banner"})
	got := r.ResolveImage(context.Background(), d)

	want := radiofrance.DiffusionImageURL(d)
	if got != want {
		t.Errorf("ResolveImage = %q, want %q (visuals fallback)", got, want)
	}
	if mainImage, ok := store.originImages["origin1"]; !ok || mainImage != "" {
		t.Errorf("expected the empty result to be cached, got %q, ok=%v", mainImage, ok)
	}
}

func TestResolveImage_RerunFallsBackToVisualsWhenOriginFetchFails(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{diffusionErr: errors.New("upstream boom")}
	r := NewResolver(store, fetcher, nil)

	d := diffusionWithOrigin("d1", "origin1", radiofrance.Visual{Name: "square_banner", VisualUUID: "uuid-show-banner"})
	got := r.ResolveImage(context.Background(), d)

	want := radiofrance.DiffusionImageURL(d)
	if got != want {
		t.Errorf("ResolveImage = %q, want %q (visuals fallback)", got, want)
	}
	if _, ok := store.originImages["origin1"]; ok {
		t.Error("did not expect a cache entry when the fetch failed")
	}
}
