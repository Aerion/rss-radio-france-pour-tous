package episodecache

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeJob is a test double for job, letting these tests control timing
// (via started/block channels) and outcome without exercising a real
// Resolver.
type fakeJob struct {
	calls   *int
	mu      *sync.Mutex
	started chan<- struct{} // optional: signaled right before blocking
	block   <-chan struct{} // optional: run() waits on this before returning
	outcome bool
	jobKind string
	gotCtx  *context.Context // optional: captures the ctx run() received
}

func (j fakeJob) run(ctx context.Context, r *Resolver) bool {
	if j.mu != nil {
		j.mu.Lock()
		*j.calls++
		j.mu.Unlock()
	}
	if j.gotCtx != nil {
		*j.gotCtx = ctx
	}
	if j.started != nil {
		j.started <- struct{}{}
	}
	if j.block != nil {
		<-j.block
	}
	return j.outcome
}

func (j fakeJob) kind() string { return j.jobKind }

func newCountingJob(outcome bool) (fakeJob, *int) {
	calls := 0
	return fakeJob{calls: &calls, mu: &sync.Mutex{}, outcome: outcome, jobKind: jobKindManifestation}, &calls
}

func TestEnricher_EnqueueDedupesWhilePending(t *testing.T) {
	e := NewEnricher(10, time.Second, nil)
	job, _ := newCountingJob(true)

	e.enqueue("key1", job)
	e.enqueue("key1", job) // still queued (nothing has drained it) - should be deduped

	if got := len(e.jobs); got != 1 {
		t.Errorf("queue length = %d, want 1 (duplicate enqueue should be a no-op)", got)
	}
}

func TestEnricher_DropsAndRollsBackPendingWhenQueueFull(t *testing.T) {
	e := NewEnricher(1, time.Second, nil)
	job, _ := newCountingJob(true)

	e.enqueue("key1", job) // fills the size-1 queue

	e.enqueue("key2", job) // queue full - should be dropped
	if e.isPending("key2") {
		t.Error("expected key2's pending marker to be rolled back after being dropped")
	}

	<-e.jobs // simulate a worker picking up key1's job, freeing queue capacity

	e.enqueue("key2", job)
	if !e.isPending("key2") {
		t.Error("expected key2 to be re-enqueueable once queue capacity freed up - the rollback must not have permanently wedged it")
	}
}

func TestEnricher_IsPendingReflectsQueuedRunningAndFinishedState(t *testing.T) {
	e := NewEnricher(10, time.Second, nil)
	started := make(chan struct{})
	block := make(chan struct{})
	job := fakeJob{calls: new(int), mu: &sync.Mutex{}, started: started, block: block, outcome: true, jobKind: jobKindManifestation}

	e.enqueue("key1", job)
	if !e.isPending("key1") {
		t.Fatal("expected key1 to be pending once queued")
	}

	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil, e, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		e.Run(ctx, r, 1)
		close(done)
	}()

	<-started // the worker has picked up the job and is now blocked in run()
	if !e.isPending("key1") {
		t.Error("expected key1 to still be pending while its job is running")
	}

	close(block)
	waitUntil(t, func() bool { return !e.isPending("key1") }, time.Second)

	cancel()
	<-done
}

func TestEnricher_RunExitsPromptlyOnContextCancellation(t *testing.T) {
	e := NewEnricher(10, time.Second, nil)
	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil, e, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		e.Run(ctx, r, 2)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit promptly after ctx cancellation")
	}
}

func TestEnricher_ProcessAppliesJobTimeout(t *testing.T) {
	e := NewEnricher(10, 50*time.Millisecond, nil)
	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil, e, time.Hour)
	started := make(chan struct{}, 1)
	var gotCtx context.Context
	job := fakeJob{calls: new(int), mu: &sync.Mutex{}, started: started, outcome: true, jobKind: jobKindManifestation, gotCtx: &gotCtx}
	e.enqueue("key1", job)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx, r, 1)

	<-started
	if gotCtx == nil {
		t.Fatal("expected the job to have captured its context")
	}
	if _, ok := gotCtx.Deadline(); !ok {
		t.Error("expected the job's context to carry a deadline from the configured job timeout")
	}
}

func TestEnricher_RecordsEnqueueAndProcessedMetrics(t *testing.T) {
	metrics := &fakeEnrichmentObserver{}
	e := NewEnricher(2, time.Second, metrics) // big enough to hold key1+key2 before Run starts draining
	succeed, _ := newCountingJob(true)
	fail, _ := newCountingJob(false)
	fail.jobKind = jobKindOrigin

	e.enqueue("key1", succeed)
	e.enqueue("key1", succeed) // duplicate
	e.enqueue("key2", fail)
	e.enqueue("key3", fail) // queue (size 2) already full with key1+key2 - dropped

	r := NewResolver(newFakeStore(), &fakeFetcher{}, nil, e, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.Run(ctx, r, 1)
		close(done)
	}()

	waitUntil(t, func() bool {
		metrics.mu.Lock()
		defer metrics.mu.Unlock()
		return len(metrics.processed) >= 2
	}, time.Second)
	cancel()
	<-done

	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if !containsAll(metrics.enqueued, "queued", "duplicate", "dropped") {
		t.Errorf("enqueued outcomes = %v, want queued/duplicate/dropped all present", metrics.enqueued)
	}
	var sawSucceeded, sawFailed bool
	for _, p := range metrics.processed {
		if p.outcome == jobOutcomeSucceeded && p.kind == jobKindManifestation {
			sawSucceeded = true
		}
		if p.outcome == jobOutcomeFailed && p.kind == jobKindOrigin {
			sawFailed = true
		}
	}
	if !sawSucceeded || !sawFailed {
		t.Errorf("processed = %v, want a succeeded/manifestation and a failed/origin entry", metrics.processed)
	}
}

// waitUntil polls cond every few milliseconds until it's true or timeout
// elapses, failing the test in the latter case. Used instead of a fixed
// sleep so these goroutine-driven tests aren't tied to a specific timing
// assumption beyond "eventually".
func waitUntil(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition was not met before the timeout")
	}
}

func containsAll(haystack []string, wants ...string) bool {
	for _, want := range wants {
		found := false
		for _, got := range haystack {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// fakeEnrichmentObserver records every event passed to it, for tests to
// assert against. Guarded by a mutex since the Enricher's worker
// goroutine(s) call it concurrently with the test's own assertions.
type fakeEnrichmentObserver struct {
	mu               sync.Mutex
	enqueued         []string
	queueDepthDeltas []int64
	processed        []struct{ kind, outcome string }
}

func (o *fakeEnrichmentObserver) ObserveEnrichmentEnqueued(ctx context.Context, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.enqueued = append(o.enqueued, outcome)
}

func (o *fakeEnrichmentObserver) AdjustEnrichmentQueueDepth(ctx context.Context, delta int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queueDepthDeltas = append(o.queueDepthDeltas, delta)
}

func (o *fakeEnrichmentObserver) ObserveEnrichmentJob(ctx context.Context, kind, outcome string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.processed = append(o.processed, struct{ kind, outcome string }{kind, outcome})
}
