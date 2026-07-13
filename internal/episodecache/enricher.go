package episodecache

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Aerion/rss-radio-france-pour-tous/internal/radiofrance"
)

// EnrichmentObserver receives the outcome of each enrichment queue/worker
// event, for queue-health monitoring (is the queue keeping up, backing up,
// or dropping work). Defined here rather than in a metrics package so this
// package stays decoupled from any particular metrics backend;
// observability.Observability implements it.
type EnrichmentObserver interface {
	ObserveEnrichmentEnqueued(ctx context.Context, outcome string)
	AdjustEnrichmentQueueDepth(ctx context.Context, delta int64)
	ObserveEnrichmentJob(ctx context.Context, kind, outcome string)
}

const (
	enqueueOutcomeQueued    = "queued"
	enqueueOutcomeDuplicate = "duplicate"
	enqueueOutcomeDropped   = "dropped"

	jobOutcomeSucceeded = "succeeded"
	jobOutcomeFailed    = "failed"

	jobKindManifestation = "manifestation"
	jobKindOrigin        = "origin"
)

// failureBackoff is how long a key that just failed enrichment is
// considered still "not resolved" for Resolver.AllResolved's purposes,
// after the failed job has already cleared its pending marker (see
// Enricher.process). Without this, a diffusion whose upstream lookup keeps
// failing (e.g. Radio France down for that item) would report as resolved
// the instant the failed job finishes, causing every subsequent request to
// invalidate and rebuild its degraded feed page - re-enqueuing, re-failing,
// and re-invalidating on essentially every request instead of settling
// into serving the stale degraded copy until upstream recovers.
const failureBackoff = 5 * time.Minute

// job is one unit of background enrichment work. Implemented as typed
// structs (manifestationJob/originJob) rather than closures so a job can
// never accidentally capture a stale request-scoped context.Context - a
// worker always supplies a fresh, independently-timed context (see
// Enricher.process). run reports whether it fully resolved what it was
// enriching (e.g. found a principal manifestation); a false return still
// means it ran to completion, just without reaching a definite result -
// every upstream failure is logged and degrades gracefully internally,
// never returned as an error.
type job interface {
	run(ctx context.Context, r *Resolver) bool
	kind() string
}

// manifestationJob resolves and caches a diffusion's principal
// manifestation - the background counterpart to Resolver.Resolve's fast
// path, run once Resolve has enqueued it on a cache miss.
type manifestationJob struct {
	showID, showTitle string
	d                 radiofrance.Diffusion
	included          map[string]radiofrance.ManifestationDetails
}

func (j manifestationJob) run(ctx context.Context, r *Resolver) bool {
	return r.enrichManifestation(ctx, j.showID, j.showTitle, j.d, j.included)
}
func (j manifestationJob) kind() string { return jobKindManifestation }

// originJob resolves and caches a rerun's origin diffusion image and body -
// the background counterpart to Resolver.ResolveImage/ResolveDescription's
// fast path, run once either has enqueued it on a cache miss.
type originJob struct {
	originID string
}

func (j originJob) run(ctx context.Context, r *Resolver) bool { return r.enrichOrigin(ctx, j.originID) }
func (j originJob) kind() string                              { return jobKindOrigin }

// queuedJob pairs a job with the dedup key it was enqueued under, so a
// worker can clear the pending marker once the job finishes.
type queuedJob struct {
	key string
	job job
}

// Enricher runs episode enrichment (manifestation/origin resolution) on a
// small, fixed-size worker pool fed by a bounded queue, so upstream Radio
// France concurrency is one deliberate, configured number instead of an
// emergent property of request volume. Resolver enqueues a job on a cache
// miss and returns a degraded fallback immediately; a worker fills in the
// cache in the background, so the next request for the same episode is a
// cache hit.
type Enricher struct {
	jobs        chan queuedJob
	pending     sync.Map // key (string) -> struct{}, set while a job is queued or running
	failedUntil sync.Map // key (string) -> time.Time, set once a job fails - see failureBackoff
	timeout     time.Duration
	metrics     EnrichmentObserver
}

// NewEnricher creates an Enricher. queueSize bounds how many jobs can be
// waiting before new ones are dropped (see enqueue). jobTimeout bounds how
// long a single job's upstream call(s) may take - radiofrance.Client runs
// on http.DefaultClient, which has no timeout of its own, and with only a
// couple of workers a hung call would otherwise tie up a shared resource,
// not just one request's goroutine. metrics may be nil to skip recording.
func NewEnricher(queueSize int, jobTimeout time.Duration, metrics EnrichmentObserver) *Enricher {
	return &Enricher{
		jobs:    make(chan queuedJob, queueSize),
		timeout: jobTimeout,
		metrics: metrics,
	}
}

// enqueue offers j for background processing, deduped by key: if a job for
// key is already queued or running, this is a no-op. If the queue is full,
// the job is dropped (logged) rather than blocking the calling request.
func (e *Enricher) enqueue(key string, j job) {
	if _, alreadyPending := e.pending.LoadOrStore(key, struct{}{}); alreadyPending {
		e.observeEnqueued(enqueueOutcomeDuplicate)
		return
	}

	select {
	case e.jobs <- queuedJob{key: key, job: j}:
		e.observeEnqueued(enqueueOutcomeQueued)
		e.adjustQueueDepth(1)
	default:
		// Queue is full - roll back the pending marker so a later call
		// (e.g. the next request touching this episode) can retry,
		// instead of permanently wedging this key: no worker ever ran it,
		// but without this rollback no future caller could re-enqueue it
		// either.
		e.pending.Delete(key)
		slog.Warn("episodecache: enrichment queue full, dropping job", "key", key)
		e.observeEnqueued(enqueueOutcomeDropped)
	}
}

// isPending reports whether key currently has a job queued or running -
// used by Resolver.AllResolved to check whether a cached feed page's
// episodes have all finished background enrichment.
func (e *Enricher) isPending(key string) bool {
	_, pending := e.pending.Load(key)
	return pending
}

// isBackingOff reports whether key's most recent enrichment job failed
// within the last failureBackoff, and hasn't been retried since - used
// together with isPending by Resolver.AllResolved.
func (e *Enricher) isBackingOff(key string) bool {
	v, ok := e.failedUntil.Load(key)
	if !ok {
		return false
	}
	if time.Now().After(v.(time.Time)) {
		e.failedUntil.Delete(key)
		return false
	}
	return true
}

// Run starts workers goroutines draining the queue against r, blocking
// until ctx is done. Meant to be started as a background goroutine. r is
// passed in here (rather than stored on Enricher at construction) so
// Enricher and Resolver - which hold references to each other, Resolver to
// enqueue jobs and Enricher to run them against a Resolver - don't need a
// setter or a circular construction step: build the Enricher, build the
// Resolver with it, then start Run with both.
func (e *Enricher) Run(ctx context.Context, r *Resolver, workers int) {
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.worker(ctx, r)
		}()
	}
	wg.Wait()
}

func (e *Enricher) worker(ctx context.Context, r *Resolver) {
	for {
		select {
		case <-ctx.Done():
			return
		case qj := <-e.jobs:
			e.adjustQueueDepth(-1)
			e.process(ctx, r, qj)
		}
	}
}

func (e *Enricher) process(ctx context.Context, r *Resolver, qj queuedJob) {
	defer e.pending.Delete(qj.key)

	jobCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	outcome := jobOutcomeFailed
	if qj.job.run(jobCtx, r) {
		outcome = jobOutcomeSucceeded
		e.failedUntil.Delete(qj.key)
	} else {
		e.failedUntil.Store(qj.key, time.Now().Add(failureBackoff))
	}
	e.observeProcessed(qj.job.kind(), outcome)
}

func (e *Enricher) observeEnqueued(outcome string) {
	if e.metrics != nil {
		e.metrics.ObserveEnrichmentEnqueued(context.Background(), outcome)
	}
}

func (e *Enricher) adjustQueueDepth(delta int64) {
	if e.metrics != nil {
		e.metrics.AdjustEnrichmentQueueDepth(context.Background(), delta)
	}
}

func (e *Enricher) observeProcessed(kind, outcome string) {
	if e.metrics != nil {
		e.metrics.ObserveEnrichmentJob(context.Background(), kind, outcome)
	}
}

// manifestationKey/originKey are the single source of truth for dedup-key
// formatting, used both when enqueuing a job and when Resolver.AllResolved
// checks whether one is still outstanding - keeping them in one place means
// the two checks can't silently drift out of sync.
func manifestationKey(diffusionID string) string { return "manifestation:" + diffusionID }
func originKey(originDiffusionID string) string  { return "origin:" + originDiffusionID }
