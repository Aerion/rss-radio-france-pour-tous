package feedcache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// fakeObserver records every event passed to it, for tests to assert
// against. Guarded by a mutex since Sweep runs on its own goroutine.
type fakeObserver struct {
	mu          sync.Mutex
	lookups     []string
	entryCounts []int64
}

func (o *fakeObserver) ObserveFeedCacheLookup(ctx context.Context, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lookups = append(o.lookups, outcome)
}

func (o *fakeObserver) SetFeedCacheEntries(ctx context.Context, count int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entryCounts = append(o.entryCounts, count)
}

func (o *fakeObserver) lastEntryCount() (int64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.entryCounts) == 0 {
		return 0, false
	}
	return o.entryCounts[len(o.entryCounts)-1], true
}

func TestGet_MissWhenNeverSet(t *testing.T) {
	c := New(NewMemStore(), time.Hour, nil)
	_, ok := c.Get(context.Background(), "show1", 0)
	if ok {
		t.Error("expected a miss for a page that was never set")
	}
}

func TestSetThenGet_Hit(t *testing.T) {
	c := New(NewMemStore(), time.Hour, nil)
	c.Set(context.Background(), "show1", 0, Entry{Body: "<rss/>", ShowID: "show1", ShowTitle: "Show One"})

	got, ok := c.Get(context.Background(), "show1", 0)
	if !ok {
		t.Fatal("expected a hit")
	}
	if got.Body != "<rss/>" || got.ShowID != "show1" || got.ShowTitle != "Show One" {
		t.Errorf("got %+v", got)
	}
}

func TestGet_Page0_MissAfterTTLExpires(t *testing.T) {
	c := New(NewMemStore(), 10*time.Millisecond, nil)
	c.Set(context.Background(), "show1", 0, Entry{Body: "<rss/>"})

	time.Sleep(20 * time.Millisecond)
	_, ok := c.Get(context.Background(), "show1", 0)
	if ok {
		t.Error("expected a miss once the TTL has elapsed")
	}
}

func TestInvalidate_RemovesEntry(t *testing.T) {
	c := New(NewMemStore(), time.Hour, nil)
	ctx := context.Background()
	c.Set(ctx, "show1", 0, Entry{Body: "<rss/>"})

	c.Invalidate(ctx, "show1", 0)

	_, ok := c.Get(ctx, "show1", 0)
	if ok {
		t.Error("expected a miss after Invalidate")
	}
}

func TestInvalidate_NoOpWhenNotPresent(t *testing.T) {
	c := New(NewMemStore(), time.Hour, nil)
	c.Invalidate(context.Background(), "never-set", 0) // should not panic
}

func TestObserver_RecordsHitAndMissLookups(t *testing.T) {
	obs := &fakeObserver{}
	c := New(NewMemStore(), time.Hour, obs)
	ctx := context.Background()

	c.Get(ctx, "show1", 0) // miss
	c.Set(ctx, "show1", 0, Entry{Body: "<rss/>"})
	c.Get(ctx, "show1", 0) // hit

	want := []string{"miss", "hit"}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.lookups) != 2 || obs.lookups[0] != want[0] || obs.lookups[1] != want[1] {
		t.Errorf("lookups = %v, want %v", obs.lookups, want)
	}
}

func TestSweep_EvictsExpiredPage0EntriesAndReportsEntryCount(t *testing.T) {
	obs := &fakeObserver{}
	c := New(NewMemStore(), 10*time.Millisecond, obs)
	c.Set(context.Background(), "show1", 0, Entry{Body: "<rss/>"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Sweep(ctx, 5*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if n, ok := obs.lastEntryCount(); ok && n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected the expired page-0 entry to be swept and the entry count reported as 0")
}

func diffusions(ids ...string) []radiofrance.Diffusion {
	ds := make([]radiofrance.Diffusion, len(ids))
	for i, id := range ids {
		ds[i] = radiofrance.Diffusion{ID: id}
	}
	return ds
}

func TestGet_DeepPage_HitWhilePage0Unchanged(t *testing.T) {
	// A short TTL so a plain TTL check would treat the deep page as stale,
	// proving the hit below comes from the anchor check rather than the
	// wall-clock TTL.
	c := New(NewMemStore(), time.Millisecond, nil)
	ctx := context.Background()

	c.Set(ctx, "show1", 0, Entry{Diffusions: diffusions("e100")})
	c.Set(ctx, "show1", 1, Entry{Body: "<rss/>page1"})

	time.Sleep(5 * time.Millisecond)

	got, ok := c.Get(ctx, "show1", 1)
	if !ok {
		t.Fatal("expected a hit: page 0's anchor hasn't changed since page 1 was cached")
	}
	if got.Body != "<rss/>page1" {
		t.Errorf("got %+v", got)
	}
}

func TestGet_DeepPage_MissWhenPage0Shifted(t *testing.T) {
	c := New(NewMemStore(), time.Hour, nil)
	ctx := context.Background()

	c.Set(ctx, "show1", 0, Entry{Diffusions: diffusions("e100")})
	c.Set(ctx, "show1", 1, Entry{Body: "<rss/>page1"})

	// A new episode landed on page 0.
	c.Set(ctx, "show1", 0, Entry{Diffusions: diffusions("e101", "e100")})

	if _, ok := c.Get(ctx, "show1", 1); ok {
		t.Error("expected a miss: page 0's first diffusion changed, so page 1 may have shifted")
	}
}

func TestGet_DeepPage_FallsBackToTTLWhenPage0NotYetCached(t *testing.T) {
	c := New(NewMemStore(), time.Hour, nil)
	ctx := context.Background()

	// Page 1 is set without page 0 ever having been cached.
	c.Set(ctx, "show1", 1, Entry{Body: "<rss/>page1"})

	got, ok := c.Get(ctx, "show1", 1)
	if !ok {
		t.Fatal("expected a hit from the TTL fallback while the anchor is unknown")
	}
	if got.Body != "<rss/>page1" {
		t.Errorf("got %+v", got)
	}
}
