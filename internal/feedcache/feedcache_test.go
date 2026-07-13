package feedcache

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeObserver records every event passed to it, for tests to assert
// against. Guarded by a mutex since Sweep runs on its own goroutine.
type fakeObserver struct {
	mu            sync.Mutex
	lookups       []string
	entriesDeltas []int64
}

func (o *fakeObserver) ObserveFeedCacheLookup(ctx context.Context, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lookups = append(o.lookups, outcome)
}

func (o *fakeObserver) AdjustFeedCacheEntries(ctx context.Context, delta int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entriesDeltas = append(o.entriesDeltas, delta)
}

func (o *fakeObserver) sum() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	var total int64
	for _, d := range o.entriesDeltas {
		total += d
	}
	return total
}

func TestGet_MissWhenNeverSet(t *testing.T) {
	c := New(time.Hour, nil)
	_, ok := c.Get(context.Background(), Key("show1", 0))
	if ok {
		t.Error("expected a miss for a key that was never set")
	}
}

func TestSetThenGet_Hit(t *testing.T) {
	c := New(time.Hour, nil)
	key := Key("show1", 0)
	c.Set(key, Entry{Body: "<rss/>", ShowID: "show1", ShowTitle: "Show One"})

	got, ok := c.Get(context.Background(), key)
	if !ok {
		t.Fatal("expected a hit")
	}
	if got.Body != "<rss/>" || got.ShowID != "show1" || got.ShowTitle != "Show One" {
		t.Errorf("got %+v", got)
	}
}

func TestGet_MissAfterTTLExpires(t *testing.T) {
	c := New(10*time.Millisecond, nil)
	key := Key("show1", 0)
	c.Set(key, Entry{Body: "<rss/>"})

	time.Sleep(20 * time.Millisecond)
	_, ok := c.Get(context.Background(), key)
	if ok {
		t.Error("expected a miss once the TTL has elapsed")
	}
}

func TestInvalidate_RemovesEntry(t *testing.T) {
	c := New(time.Hour, nil)
	key := Key("show1", 0)
	c.Set(key, Entry{Body: "<rss/>"})

	c.Invalidate(key)

	_, ok := c.Get(context.Background(), key)
	if ok {
		t.Error("expected a miss after Invalidate")
	}
}

func TestInvalidate_NoOpWhenNotPresent(t *testing.T) {
	c := New(time.Hour, nil)
	c.Invalidate(Key("never-set", 0)) // should not panic
}

func TestKey_DistinguishesShowAndPage(t *testing.T) {
	if Key("show1", 0) == Key("show1", 1) {
		t.Error("expected different pages of the same show to have different keys")
	}
	if Key("show1", 0) == Key("show2", 0) {
		t.Error("expected different shows to have different keys")
	}
}

func TestObserver_RecordsHitAndMissLookups(t *testing.T) {
	obs := &fakeObserver{}
	c := New(time.Hour, obs)
	key := Key("show1", 0)

	c.Get(context.Background(), key) // miss
	c.Set(key, Entry{Body: "<rss/>"})
	c.Get(context.Background(), key) // hit

	want := []string{"miss", "hit"}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.lookups) != 2 || obs.lookups[0] != want[0] || obs.lookups[1] != want[1] {
		t.Errorf("lookups = %v, want %v", obs.lookups, want)
	}
}

func TestObserver_TracksEntryCountAcrossSetAndInvalidate(t *testing.T) {
	obs := &fakeObserver{}
	c := New(time.Hour, obs)
	key := Key("show1", 0)

	c.Set(key, Entry{Body: "<rss/>"})   // +1
	c.Set(key, Entry{Body: "<rss/>v2"}) // overwrite, not a new entry
	c.Invalidate(key)                   // -1

	if got := obs.sum(); got != 0 {
		t.Errorf("net entries delta = %d, want 0 (added once, removed once)", got)
	}
}

func TestSweep_EvictsExpiredEntriesAndUpdatesEntryCount(t *testing.T) {
	obs := &fakeObserver{}
	c := New(10*time.Millisecond, obs)
	c.Set(Key("show1", 0), Entry{Body: "<rss/>"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Sweep(ctx, 5*time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.entries)
		c.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected the expired entry to be swept within the deadline")
}
