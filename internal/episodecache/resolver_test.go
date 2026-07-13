package episodecache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

const testMaxAge = 30 * 24 * time.Hour

// newTestEnricher returns an Enricher with a generous queue that's never
// drained by a worker (no Run call) - good enough for tests that only care
// whether Resolve/ResolveImage/ResolveDescription enqueued the right job,
// not for what a worker would do with it.
func newTestEnricher() *Enricher {
	return NewEnricher(100, time.Second, nil)
}

// fakeStore is an in-memory store for testing Resolver's logic without a
// real database.
type fakeStore struct {
	entries       map[string]Entry
	gets          int
	getManyCalls  int
	upserts       int
	originImages  map[string]string
	originGets    int
	originUpserts int

	originBodies       map[string]string
	originStandfirsts  map[string]string
	originCreatedTimes map[string]int64
	originBodySet      map[string]bool
	originBodyGets     int
	originBodyUpserts  int

	originTitles      map[string]string
	originTitleGets   int
	originTitleUpsert int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		entries:            map[string]Entry{},
		originImages:       map[string]string{},
		originBodies:       map[string]string{},
		originStandfirsts:  map[string]string{},
		originCreatedTimes: map[string]int64{},
		originBodySet:      map[string]bool{},
		originTitles:       map[string]string{},
	}
}

func (s *fakeStore) Get(ctx context.Context, manifestationID string) (Entry, bool, error) {
	s.gets++
	e, ok := s.entries[manifestationID]
	return e, ok, nil
}

func (s *fakeStore) GetMany(ctx context.Context, manifestationIDs []string) (map[string]Entry, error) {
	s.getManyCalls++
	entries := make(map[string]Entry, len(manifestationIDs))
	for _, id := range manifestationIDs {
		if e, ok := s.entries[id]; ok {
			entries[id] = e
		}
	}
	return entries, nil
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

func (s *fakeStore) GetOriginBody(ctx context.Context, diffusionID string) (string, string, int64, bool, error) {
	s.originBodyGets++
	ok := s.originBodySet[diffusionID]
	return s.originBodies[diffusionID], s.originStandfirsts[diffusionID], s.originCreatedTimes[diffusionID], ok, nil
}

func (s *fakeStore) UpsertOriginBody(ctx context.Context, diffusionID, bodyMarkdown, standfirst string, createdTime int64) error {
	s.originBodyUpserts++
	s.originBodies[diffusionID] = bodyMarkdown
	s.originStandfirsts[diffusionID] = standfirst
	s.originCreatedTimes[diffusionID] = createdTime
	s.originBodySet[diffusionID] = true
	return nil
}

func (s *fakeStore) GetOriginTitle(ctx context.Context, diffusionID string) (string, bool, error) {
	s.originTitleGets++
	title, ok := s.originTitles[diffusionID]
	return title, ok, nil
}

func (s *fakeStore) UpsertOriginTitle(ctx context.Context, diffusionID, title string) error {
	s.originTitleUpsert++
	s.originTitles[diffusionID] = title
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

	diffusionManifestations      map[string]map[string]radiofrance.ManifestationDetails
	diffusionManifestationsErr   error
	diffusionManifestationsCalls int

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

func (f *fakeFetcher) GetDiffusionManifestations(ctx context.Context, diffusionID string) (map[string]radiofrance.ManifestationDetails, error) {
	f.diffusionManifestationsCalls++
	if f.diffusionManifestationsErr != nil {
		return nil, f.diffusionManifestationsErr
	}
	return f.diffusionManifestations[diffusionID], nil
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

// --- Resolve: fast path (no upstream calls, ever) ---

func TestResolve_PrefersPrincipalFromIncluded(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{} // should not be called at all
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	included := map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Duration: 90 * time.Second, Principal: false},
		"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 91 * time.Second, Principal: true},
		"m3": {URL: "https://cdn.example.com/m3.mp3", Duration: 92 * time.Second, Principal: false},
	}

	manifestationID, url, duration, _ := r.Resolve(context.Background(), "show1", "Show One", d, included)

	if manifestationID != "m2" {
		t.Errorf("manifestationID = %q, want m2 (the principal one)", manifestationID)
	}
	if url != "https://cdn.example.com/m2.mp3" {
		t.Errorf("url = %q, want m2's URL", url)
	}
	if duration != 91*time.Second {
		t.Errorf("duration = %v, want 91s", duration)
	}
	if fetcher.calls != 0 || fetcher.diffusionManifestationsCalls != 0 {
		t.Errorf("fetcher calls = %d/%d, want 0/0 (principal was already in included data)", fetcher.calls, fetcher.diffusionManifestationsCalls)
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
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	// included has data, but none of it is principal (and m2 - the actual
	// cached principal - isn't in included at all this time).
	included := map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Principal: false},
		"m3": {URL: "https://cdn.example.com/m3.mp3", Principal: false},
	}

	manifestationID, url, duration, _ := r.Resolve(context.Background(), "show1", "Show One", d, included)

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
	if store.getManyCalls != 1 || store.gets != 0 {
		t.Errorf("store.getManyCalls = %d, store.gets = %d, want 1 batched call and 0 individual Get calls", store.getManyCalls, store.gets)
	}
}

func TestFindCachedPrincipal_BatchesCandidatesInOneStoreRoundTrip(t *testing.T) {
	store := newFakeStore()
	// The actual principal (m5) is last in candidate order and the only
	// one that's cached - a sequential per-candidate lookup would issue 5
	// Get calls to find it; batching should need exactly one GetMany call
	// regardless of where in the list the hit is.
	store.entries["m5"] = Entry{
		ManifestationID: "m5", Principal: true, DiffusionUpdatedTime: 100,
		URL: "https://cdn.example.com/m5.mp3", FetchedAt: time.Now(),
	}
	r := NewResolver(store, &fakeFetcher{}, nil, newTestEnricher(), testMaxAge)

	id, entry, ok := r.findCachedPrincipal(context.Background(), []string{"m1", "m2", "m3", "m4", "m5"}, 100)

	if !ok || id != "m5" {
		t.Fatalf("findCachedPrincipal = (%q, %v, %v), want (m5, _, true)", id, entry, ok)
	}
	if store.getManyCalls != 1 {
		t.Errorf("store.getManyCalls = %d, want 1", store.getManyCalls)
	}
	if store.gets != 0 {
		t.Errorf("store.gets = %d, want 0 (should not fall back to per-candidate Get)", store.gets)
	}
}

func TestResolve_CacheMissEnqueuesManifestationJobAndReturnsDegradedFallback(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{} // should not be called - the whole point of the fast path
	enricher := newTestEnricher()
	r := NewResolver(store, fetcher, nil, enricher, testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	manifestationID, url, duration, _ := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "m1" {
		t.Errorf("manifestationID = %q, want m1 (d.ManifestationID() fallback)", manifestationID)
	}
	if url != "" || duration != 0 {
		t.Errorf("got url=%q duration=%v, want both zero - resolution happens in the background", url, duration)
	}
	if fetcher.calls != 0 || fetcher.diffusionManifestationsCalls != 0 {
		t.Errorf("fetcher calls = %d/%d, want 0/0 (nothing fetched synchronously)", fetcher.calls, fetcher.diffusionManifestationsCalls)
	}
	if !enricher.isPending(manifestationKey("d1")) {
		t.Error("expected a manifestation job to be enqueued for d1")
	}
}

func TestResolve_NoManifestationReturnsEmpty(t *testing.T) {
	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil, newTestEnricher(), testMaxAge)

	d := radiofrance.Diffusion{ID: "d1"} // no Relationships.Manifestations
	manifestationID, url, duration, _ := r.Resolve(context.Background(), "show1", "Show One", d, nil)

	if manifestationID != "" || url != "" || duration != 0 {
		t.Errorf("got (%q, %q, %v), want (\"\", \"\", 0)", manifestationID, url, duration)
	}
}

// --- enrichManifestation: background enrichment path ---

func TestEnrichManifestation_ResolvesInlineViaSingleBulkCall(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{diffusionManifestations: map[string]map[string]radiofrance.ManifestationDetails{
		"d1": {
			"m1": {URL: "https://cdn.example.com/m1.mp3", Duration: 10 * time.Second, Principal: false},
			"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 20 * time.Second, Principal: true},
		},
	}}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	ok := r.enrichManifestation(context.Background(), "show1", "Show One", d, nil)

	if !ok {
		t.Fatal("expected enrichManifestation to report a principal was found")
	}
	if fetcher.diffusionManifestationsCalls != 1 {
		t.Errorf("diffusionManifestationsCalls = %d, want 1", fetcher.diffusionManifestationsCalls)
	}
	if fetcher.calls != 0 {
		t.Errorf("fetcher.calls (per-ID) = %d, want 0 (bulk call already found a principal)", fetcher.calls)
	}
	if !store.entries["m2"].Principal {
		t.Error("expected m2 to be cached as principal")
	}
}

func TestEnrichManifestation_PartialBulkCoverageFallsBackPerID(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{
		diffusionManifestations: map[string]map[string]radiofrance.ManifestationDetails{
			"d1": {"m1": {URL: "https://cdn.example.com/m1.mp3", Principal: false}},
		},
		details: map[string]radiofrance.ManifestationDetails{
			"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 20 * time.Second, Principal: true},
		},
	}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2", "m3")
	ok := r.enrichManifestation(context.Background(), "show1", "Show One", d, nil)

	if !ok {
		t.Fatal("expected enrichManifestation to report a principal was found")
	}
	if fetcher.diffusionManifestationsCalls != 1 {
		t.Errorf("diffusionManifestationsCalls = %d, want 1", fetcher.diffusionManifestationsCalls)
	}
	if fetcher.calls != 1 || len(fetcher.callIDs) != 1 || fetcher.callIDs[0] != "m2" {
		t.Errorf("per-ID calls = %v, want exactly [m2] (m1 already covered by the bulk call, m3 never reached since m2 was principal)", fetcher.callIDs)
	}
}

func TestEnrichManifestation_CachesEveryResolvedCandidateEvenWithoutPrincipal(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/m1.mp3", Duration: 10 * time.Second, Principal: false},
		"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 20 * time.Second, Principal: false},
	}}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	ok := r.enrichManifestation(context.Background(), "show1", "Show One", d, nil)

	if ok {
		t.Error("expected enrichManifestation to report no principal was found")
	}
	if fetcher.calls != 2 {
		t.Errorf("fetcher.calls = %d, want 2 (tried every candidate looking for principal)", fetcher.calls)
	}
	if store.upserts != 2 {
		t.Errorf("store.upserts = %d, want 2 (both non-principal candidates still cached)", store.upserts)
	}
}

func TestEnrichManifestation_AllFetchesFailCachesNothing(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{err: errors.New("upstream boom")}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	ok := r.enrichManifestation(context.Background(), "show1", "Show One", d, nil)

	if ok {
		t.Error("expected enrichManifestation to report failure")
	}
	if store.upserts != 0 {
		t.Errorf("store.upserts = %d, want 0 (nothing to cache, everything failed)", store.upserts)
	}
}

func TestEnrichManifestation_SkipsFailingCandidateAndTriesNext(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{
		errByID: map[string]error{"m1": errors.New("gone")},
		details: map[string]radiofrance.ManifestationDetails{
			"m2": {URL: "https://cdn.example.com/m2.mp3", Duration: 20 * time.Second, Principal: true},
		},
	}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	ok := r.enrichManifestation(context.Background(), "show1", "Show One", d, nil)

	if !ok {
		t.Fatal("expected enrichManifestation to report a principal was found (m1 failed, m2 succeeded)")
	}
	if !store.entries["m2"].Principal {
		t.Error("expected m2 to be cached as principal")
	}
}

func TestEnrichManifestation_RechecksCacheBeforeFetching(t *testing.T) {
	store := newFakeStore()
	store.entries["m2"] = Entry{
		ManifestationID: "m2", Principal: true, DiffusionUpdatedTime: 100,
		URL: "https://cdn.example.com/m2-cached.mp3", Duration: 91 * time.Second, FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{} // should not be called at all
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1", "m2")
	ok := r.enrichManifestation(context.Background(), "show1", "Show One", d, nil)

	if !ok {
		t.Fatal("expected enrichManifestation to report success (already resolved by a concurrent request)")
	}
	if fetcher.calls != 0 || fetcher.diffusionManifestationsCalls != 0 {
		t.Error("expected no upstream calls when the cache already had a fresh principal")
	}
}

func TestEnrichManifestation_NoManifestationsIsNoop(t *testing.T) {
	fetcher := &fakeFetcher{}
	r := NewResolver(newFakeStore(), fetcher, nil, newTestEnricher(), testMaxAge)

	ok := r.enrichManifestation(context.Background(), "show1", "Show One", radiofrance.Diffusion{ID: "d1"}, nil)
	if ok {
		t.Error("expected false when there are no manifestation candidates")
	}
	if fetcher.calls != 0 || fetcher.diffusionManifestationsCalls != 0 {
		t.Error("expected no upstream calls")
	}
}

// --- ResolveAudioURL: untouched by this redesign, still fetches live on a miss ---

func TestResolveAudioURL_CacheHit(t *testing.T) {
	store := newFakeStore()
	store.entries["m1"] = Entry{
		ManifestationID: "m1", URL: "https://cdn.example.com/a.mp3",
		ShowID: "show1", ShowTitle: "Show One", FetchedAt: time.Now(),
	}
	fetcher := &fakeFetcher{}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

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
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

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
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

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
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

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
	r := NewResolver(store, fetcher, observer, newTestEnricher(), testMaxAge)

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

// syncFakeStore wraps fakeStore with a mutex, since fakeStore's own
// counters aren't safe for concurrent access - needed for
// TestResolveAudioURL_DedupesConcurrentColdFetches, which calls
// ResolveAudioURL from several goroutines at once (store.Get runs on every
// caller, only the singleflight-selected winner's store.Upsert should ever
// run, but Get itself is still genuinely concurrent here).
type syncFakeStore struct {
	mu sync.Mutex
	*fakeStore
}

func newSyncFakeStore() *syncFakeStore {
	return &syncFakeStore{fakeStore: newFakeStore()}
}

func (s *syncFakeStore) Get(ctx context.Context, manifestationID string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.Get(ctx, manifestationID)
}

func (s *syncFakeStore) Upsert(ctx context.Context, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.Upsert(ctx, e)
}

func (s *syncFakeStore) GetMany(ctx context.Context, manifestationIDs []string) (map[string]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeStore.GetMany(ctx, manifestationIDs)
}

// blockingFetcher wraps fakeFetcher, blocking GetManifestation until
// release is closed, and signaling started on entry - used to widen the
// race window in TestResolveAudioURL_DedupesConcurrentColdFetches so
// several goroutines reliably arrive before the first upstream call
// completes.
type blockingFetcher struct {
	*fakeFetcher
	started chan struct{}
	release chan struct{}
}

func (f *blockingFetcher) GetManifestation(ctx context.Context, manifestationID string) (radiofrance.ManifestationDetails, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return f.fakeFetcher.GetManifestation(ctx, manifestationID)
}

func TestResolveAudioURL_DedupesConcurrentColdFetches(t *testing.T) {
	store := newSyncFakeStore()
	inner := &fakeFetcher{details: map[string]radiofrance.ManifestationDetails{
		"m1": {URL: "https://cdn.example.com/a.mp3"},
	}}
	fetcher := &blockingFetcher{fakeFetcher: inner, started: make(chan struct{}, 1), release: make(chan struct{})}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	const n = 10
	var wg sync.WaitGroup
	urls := make([]string, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			url, _, _, err := r.ResolveAudioURL(context.Background(), "m1")
			urls[i] = url
			errs[i] = err
		}(i)
	}

	<-fetcher.started                 // at least one goroutine has entered the upstream call
	time.Sleep(20 * time.Millisecond) // give the rest time to queue up behind singleflight
	close(fetcher.release)
	wg.Wait()

	if inner.calls != 1 {
		t.Errorf("fetcher.calls = %d, want 1 (concurrent cold requests for the same manifestation should be deduped)", inner.calls)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: ResolveAudioURL: %v", i, err)
		}
		if urls[i] != "https://cdn.example.com/a.mp3" {
			t.Errorf("goroutine %d: url = %q", i, urls[i])
		}
	}
}

func diffusionWithOrigin(id, originID string, visuals ...radiofrance.Visual) radiofrance.Diffusion {
	d := radiofrance.Diffusion{ID: id, Visuals: visuals}
	if originID != "" {
		d.Relationships.OriginDiffusion = []string{originID}
	}
	return d
}

// --- ResolveImage: fast path ---

func TestResolveImage_UsesMainImageDirectlyWhenPresent(t *testing.T) {
	fetcher := &fakeFetcher{} // should not be called at all
	r := NewResolver(newFakeStore(), fetcher, nil, newTestEnricher(), testMaxAge)

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
	r := NewResolver(newFakeStore(), fetcher, nil, newTestEnricher(), testMaxAge)

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

func TestResolveImage_RerunUsesCachedOriginImageWithoutFetching(t *testing.T) {
	store := newFakeStore()
	store.originImages["origin1"] = "uuid-episode-cached"
	fetcher := &fakeFetcher{} // should not be called
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

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

func TestResolveImage_RerunCacheMissEnqueuesOriginJobAndFallsBackToVisuals(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{} // should not be called - the whole point of the fast path
	enricher := newTestEnricher()
	r := NewResolver(store, fetcher, nil, enricher, testMaxAge)

	d := diffusionWithOrigin("d1", "origin1", radiofrance.Visual{Name: "square_banner", VisualUUID: "uuid-show-banner"})
	got := r.ResolveImage(context.Background(), d)

	want := radiofrance.DiffusionImageURL(d)
	if got != want {
		t.Errorf("ResolveImage = %q, want %q (visuals fallback while enrichment is pending)", got, want)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (nothing fetched synchronously)", fetcher.diffusionCalls)
	}
	if !enricher.isPending(originKey("origin1")) {
		t.Error("expected an origin job to be enqueued for origin1")
	}
}

// --- ResolveDescription: fast path ---

func TestResolveDescription_NoOriginUsesOwnFields(t *testing.T) {
	fetcher := &fakeFetcher{} // should not be called at all
	r := NewResolver(newFakeStore(), fetcher, nil, newTestEnricher(), testMaxAge)

	d := radiofrance.Diffusion{ID: "d1", BodyMarkdown: "own body", Standfirst: "own standfirst"}
	body, sf, originCreatedTime := r.ResolveDescription(context.Background(), d)

	if body != "own body" || sf != "own standfirst" {
		t.Errorf("ResolveDescription = (%q, %q), want own fields", body, sf)
	}
	if originCreatedTime != 0 {
		t.Errorf("originCreatedTime = %d, want 0 (not a rerun)", originCreatedTime)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (not a rerun)", fetcher.diffusionCalls)
	}
}

func TestResolveDescription_RerunUsesCachedOriginBodyWithoutFetching(t *testing.T) {
	store := newFakeStore()
	store.originBodies["origin1"] = "cached origin body"
	store.originStandfirsts["origin1"] = "cached origin standfirst"
	store.originCreatedTimes["origin1"] = 1704067200
	store.originBodySet["origin1"] = true
	fetcher := &fakeFetcher{} // should not be called
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := radiofrance.Diffusion{ID: "d1"}
	d.Relationships.OriginDiffusion = []string{"origin1"}
	body, sf, originCreatedTime := r.ResolveDescription(context.Background(), d)

	if body != "cached origin body" || sf != "cached origin standfirst" {
		t.Errorf("ResolveDescription = (%q, %q), want cached origin fields", body, sf)
	}
	if originCreatedTime != 1704067200 {
		t.Errorf("originCreatedTime = %d, want the cached CreatedTime", originCreatedTime)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (cache hit)", fetcher.diffusionCalls)
	}
}

func TestResolveDescription_RerunUsesCachedButBlankOriginBodyFallsBackToOwnFields(t *testing.T) {
	store := newFakeStore()
	// The origin diffusion was already resolved by a prior background
	// job - it just turned out to have no real notes of its own (a
	// placeholder), though its CreatedTime is real.
	store.originBodies["origin1"] = "."
	store.originStandfirsts["origin1"] = "."
	store.originCreatedTimes["origin1"] = 1704067200
	store.originBodySet["origin1"] = true
	fetcher := &fakeFetcher{} // should not be called
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := radiofrance.Diffusion{ID: "d1", BodyMarkdown: "own body", Standfirst: "own standfirst"}
	d.Relationships.OriginDiffusion = []string{"origin1"}
	body, sf, originCreatedTime := r.ResolveDescription(context.Background(), d)

	if body != "own body" || sf != "own standfirst" {
		t.Errorf("ResolveDescription = (%q, %q), want own fields (origin blank)", body, sf)
	}
	if originCreatedTime != 1704067200 {
		t.Errorf("originCreatedTime = %d, want the origin's CreatedTime even though its body was blank", originCreatedTime)
	}
}

func TestResolveDescription_RerunCacheMissEnqueuesOriginJobAndFallsBackToOwnFields(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{} // should not be called - the whole point of the fast path
	enricher := newTestEnricher()
	r := NewResolver(store, fetcher, nil, enricher, testMaxAge)

	d := radiofrance.Diffusion{ID: "d1", BodyMarkdown: "own body", Standfirst: "own standfirst"}
	d.Relationships.OriginDiffusion = []string{"origin1"}
	body, sf, originCreatedTime := r.ResolveDescription(context.Background(), d)

	if body != "own body" || sf != "own standfirst" {
		t.Errorf("ResolveDescription = (%q, %q), want own fields while enrichment is pending", body, sf)
	}
	if originCreatedTime != 0 {
		t.Errorf("originCreatedTime = %d, want 0 (origin not resolved yet)", originCreatedTime)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (nothing fetched synchronously)", fetcher.diffusionCalls)
	}
	if !enricher.isPending(originKey("origin1")) {
		t.Error("expected an origin job to be enqueued for origin1")
	}
}

func TestResolveImageAndDescription_ShareTheSameOriginJob(t *testing.T) {
	store := newFakeStore()
	enricher := newTestEnricher()
	r := NewResolver(store, &fakeFetcher{}, nil, enricher, testMaxAge)

	d := diffusionWithOrigin("d1", "origin1", radiofrance.Visual{Name: "square_banner", VisualUUID: "uuid-show-banner"})
	r.ResolveImage(context.Background(), d)
	r.ResolveDescription(context.Background(), d)
	r.ResolveTitle(context.Background(), d)

	if got := len(enricher.jobs); got != 1 {
		t.Errorf("queued job count = %d, want 1 (all three should enqueue the same deduped origin job)", got)
	}
}

// --- ResolveTitle ---

func TestResolveTitle_NoOriginUsesOwnTitle(t *testing.T) {
	fetcher := &fakeFetcher{} // should not be called at all
	r := NewResolver(newFakeStore(), fetcher, nil, newTestEnricher(), testMaxAge)

	d := radiofrance.Diffusion{ID: "d1", Title: "own title"}
	title := r.ResolveTitle(context.Background(), d)

	if title != "own title" {
		t.Errorf("ResolveTitle = %q, want own title", title)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (not a rerun)", fetcher.diffusionCalls)
	}
}

func TestResolveTitle_RerunUsesCachedOriginTitleWithoutFetching(t *testing.T) {
	store := newFakeStore()
	store.originTitles["origin1"] = "real episode title"
	fetcher := &fakeFetcher{} // should not be called
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := radiofrance.Diffusion{ID: "d1", Title: "generic rerun slot title"}
	d.Relationships.OriginDiffusion = []string{"origin1"}
	title := r.ResolveTitle(context.Background(), d)

	if title != "real episode title" {
		t.Errorf("ResolveTitle = %q, want cached origin title", title)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (cache hit)", fetcher.diffusionCalls)
	}
}

func TestResolveTitle_RerunUsesCachedButBlankOriginTitleFallsBackToOwnTitle(t *testing.T) {
	store := newFakeStore()
	store.originTitles["origin1"] = "."
	fetcher := &fakeFetcher{} // should not be called
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	d := radiofrance.Diffusion{ID: "d1", Title: "generic rerun slot title"}
	d.Relationships.OriginDiffusion = []string{"origin1"}
	title := r.ResolveTitle(context.Background(), d)

	if title != "generic rerun slot title" {
		t.Errorf("ResolveTitle = %q, want own title (origin blank)", title)
	}
}

func TestResolveTitle_RerunCacheMissEnqueuesOriginJobAndFallsBackToOwnTitle(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{} // should not be called - the whole point of the fast path
	enricher := newTestEnricher()
	r := NewResolver(store, fetcher, nil, enricher, testMaxAge)

	d := radiofrance.Diffusion{ID: "d1", Title: "generic rerun slot title"}
	d.Relationships.OriginDiffusion = []string{"origin1"}
	title := r.ResolveTitle(context.Background(), d)

	if title != "generic rerun slot title" {
		t.Errorf("ResolveTitle = %q, want own title while enrichment is pending", title)
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (nothing fetched synchronously)", fetcher.diffusionCalls)
	}
	if !enricher.isPending(originKey("origin1")) {
		t.Error("expected an origin job to be enqueued for origin1")
	}
}

// --- enrichOrigin: background enrichment path ---

func TestEnrichOrigin_CachesMainImageAndBodyAndTitleViaSingleCall(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{diffusions: map[string]radiofrance.Diffusion{
		"origin1": {ID: "origin1", Title: "Real episode title", MainImage: "uuid-episode", BodyMarkdown: "**rich** origin body", Standfirst: "origin standfirst", CreatedTime: 1704067200},
	}}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	ok := r.enrichOrigin(context.Background(), "origin1")

	if !ok {
		t.Fatal("expected enrichOrigin to report success")
	}
	if fetcher.diffusionCalls != 1 {
		t.Errorf("fetcher.diffusionCalls = %d, want 1 (single call resolves image, body, and title)", fetcher.diffusionCalls)
	}
	if store.originImages["origin1"] != "uuid-episode" {
		t.Errorf("store.originImages[origin1] = %q, want it cached", store.originImages["origin1"])
	}
	if store.originBodies["origin1"] != "**rich** origin body" {
		t.Errorf("store.originBodies[origin1] = %q, want it cached", store.originBodies["origin1"])
	}
	if store.originCreatedTimes["origin1"] != 1704067200 {
		t.Errorf("store.originCreatedTimes[origin1] = %d, want it cached", store.originCreatedTimes["origin1"])
	}
	if store.originTitles["origin1"] != "Real episode title" {
		t.Errorf("store.originTitles[origin1] = %q, want it cached", store.originTitles["origin1"])
	}
}

func TestEnrichOrigin_NoOpWhenAllAlreadyCached(t *testing.T) {
	store := newFakeStore()
	store.originImages["origin1"] = "uuid-episode"
	store.originBodySet["origin1"] = true
	store.originTitles["origin1"] = "Real episode title"
	fetcher := &fakeFetcher{} // should not be called
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	ok := r.enrichOrigin(context.Background(), "origin1")

	if !ok {
		t.Error("expected enrichOrigin to report success (already cached)")
	}
	if fetcher.diffusionCalls != 0 {
		t.Errorf("fetcher.diffusionCalls = %d, want 0 (all already cached)", fetcher.diffusionCalls)
	}
}

func TestEnrichOrigin_CachesEmptyImageWhenOriginHasNone(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{diffusions: map[string]radiofrance.Diffusion{
		"origin1": {ID: "origin1"}, // no MainImage
	}}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	ok := r.enrichOrigin(context.Background(), "origin1")

	if !ok {
		t.Error("expected enrichOrigin to report success")
	}
	if mainImage, ok := store.originImages["origin1"]; !ok || mainImage != "" {
		t.Errorf("expected the empty result to be cached, got %q, ok=%v", mainImage, ok)
	}
}

func TestEnrichOrigin_FetchFailureLeavesCacheEmpty(t *testing.T) {
	store := newFakeStore()
	fetcher := &fakeFetcher{diffusionErr: errors.New("upstream boom")}
	r := NewResolver(store, fetcher, nil, newTestEnricher(), testMaxAge)

	ok := r.enrichOrigin(context.Background(), "origin1")

	if ok {
		t.Error("expected enrichOrigin to report failure")
	}
	if _, ok := store.originImages["origin1"]; ok {
		t.Error("did not expect a cache entry when the fetch failed")
	}
	if _, ok := store.originBodySet["origin1"]; ok {
		t.Error("did not expect a cache entry when the fetch failed")
	}
}

// --- AllResolved ---

func TestAllResolved_NothingPendingReturnsTrue(t *testing.T) {
	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil, newTestEnricher(), testMaxAge)

	d := diffusionWithManifestation("d1", "m1", 100)
	if !r.AllResolved([]radiofrance.Diffusion{d}) {
		t.Error("expected AllResolved to be true when nothing is pending")
	}
}

func TestAllResolved_PendingManifestationReturnsFalse(t *testing.T) {
	store := newFakeStore()
	enricher := newTestEnricher()
	r := NewResolver(store, &fakeFetcher{}, nil, enricher, testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1")
	r.Resolve(context.Background(), "show1", "Show One", d, nil) // enqueues a manifestation job

	if r.AllResolved([]radiofrance.Diffusion{d}) {
		t.Error("expected AllResolved to be false while the manifestation job is still pending")
	}
}

func TestAllResolved_PendingOriginOfRerunReturnsFalse(t *testing.T) {
	store := newFakeStore()
	enricher := newTestEnricher()
	r := NewResolver(store, &fakeFetcher{}, nil, enricher, testMaxAge)

	d := diffusionWithOrigin("d1", "origin1")
	d.Relationships.Manifestations = []string{"m1"}
	store.entries["m1"] = Entry{
		ManifestationID: "m1", Principal: true, DiffusionUpdatedTime: 0,
		URL: "https://cdn.example.com/m1.mp3", FetchedAt: time.Now(),
	}
	r.ResolveImage(context.Background(), d) // enqueues an origin job

	if r.AllResolved([]radiofrance.Diffusion{d}) {
		t.Error("expected AllResolved to be false while the origin job is still pending")
	}
}

func TestAllResolved_RecentlyFailedManifestationReturnsFalse(t *testing.T) {
	enricher := newTestEnricher()
	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil, enricher, testMaxAge)

	d := diffusionWithManifestations("d1", 100, "m1")
	// Simulate a job for d1 having already run and failed, as
	// Enricher.process would leave it - nothing pending, but still
	// backing off.
	enricher.failedUntil.Store(manifestationKey("d1"), time.Now().Add(failureBackoff))

	if r.AllResolved([]radiofrance.Diffusion{d}) {
		t.Error("expected AllResolved to be false while the manifestation is backing off from a recent failure")
	}
}
