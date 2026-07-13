package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestObservability(t *testing.T) *Observability {
	t.Helper()
	o, err := New("test-service")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = o.Shutdown(ctx)
	})
	return o
}

func TestWrap_RecordsRequestMetrics(t *testing.T) {
	o := newTestObservability(t)
	handler := o.Wrap("rss", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/rss/some-show", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `http_requests_total{method="GET",route="rss",status="404"} 1`) {
		t.Errorf("expected http_requests_total counter for rss/GET/404, got:\n%s", body)
	}
}

func TestWrap_DefaultsToStatus200WhenNotSetExplicitly(t *testing.T) {
	o := newTestObservability(t)
	handler := o.Wrap("home", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) // no explicit WriteHeader call
	})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `http_requests_total{method="GET",route="home",status="200"} 1`) {
		t.Errorf("expected http_requests_total counter for home/GET/200, got:\n%s", body)
	}
}

func TestObserveRequest_RecordsRadioFranceMetrics(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.ObserveRequest(ctx, "diffusions", true, 42*time.Millisecond)
	o.ObserveRequest(ctx, "diffusions", false, 10*time.Millisecond)

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `radiofrance_api_requests_total{endpoint="diffusions",status="ok"} 1`) {
		t.Errorf("expected an ok counter for diffusions, got:\n%s", body)
	}
	if !strings.Contains(body, `radiofrance_api_requests_total{endpoint="diffusions",status="error"} 1`) {
		t.Errorf("expected an error counter for diffusions, got:\n%s", body)
	}
}

func TestObserveRequestStarted_RecordsRadioFranceMetric(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.ObserveRequestStarted(ctx, "diffusions")
	o.ObserveRequestStarted(ctx, "diffusions")

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `radiofrance_api_requests_started_total{endpoint="diffusions"} 2`) {
		t.Errorf("expected a started counter of 2 for diffusions, got:\n%s", body)
	}
}

func TestObserveFeedCacheLookup_RecordsOutcome(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.ObserveFeedCacheLookup(ctx, "hit")
	o.ObserveFeedCacheLookup(ctx, "miss")
	o.ObserveFeedCacheLookup(ctx, "miss")

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `feed_cache_lookups_total{outcome="hit"} 1`) {
		t.Errorf("expected a hit=1 counter, got:\n%s", body)
	}
	if !strings.Contains(body, `feed_cache_lookups_total{outcome="miss"} 2`) {
		t.Errorf("expected a miss=2 counter, got:\n%s", body)
	}
}

func TestAdjustFeedCacheEntries_TracksCurrentCount(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.AdjustFeedCacheEntries(ctx, 1)
	o.AdjustFeedCacheEntries(ctx, 1)
	o.AdjustFeedCacheEntries(ctx, -1)

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, "feed_cache_entries 1") {
		t.Errorf("expected feed_cache_entries = 1, got:\n%s", body)
	}
}

func TestObserveEnrichmentEnqueued_RecordsOutcome(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.ObserveEnrichmentEnqueued(ctx, "queued")
	o.ObserveEnrichmentEnqueued(ctx, "duplicate")
	o.ObserveEnrichmentEnqueued(ctx, "dropped")

	body := scrapeMetrics(t, o)
	for _, outcome := range []string{"queued", "duplicate", "dropped"} {
		if !strings.Contains(body, `enrichment_queue_enqueued_total{outcome="`+outcome+`"} 1`) {
			t.Errorf("expected a %s=1 counter, got:\n%s", outcome, body)
		}
	}
}

func TestAdjustEnrichmentQueueDepth_TracksCurrentDepth(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.AdjustEnrichmentQueueDepth(ctx, 1)
	o.AdjustEnrichmentQueueDepth(ctx, 1)
	o.AdjustEnrichmentQueueDepth(ctx, 1)
	o.AdjustEnrichmentQueueDepth(ctx, -1)

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, "enrichment_queue_depth 2") {
		t.Errorf("expected enrichment_queue_depth = 2, got:\n%s", body)
	}
}

func TestObserveEnrichmentJob_RecordsKindAndOutcome(t *testing.T) {
	o := newTestObservability(t)
	ctx := context.Background()
	o.ObserveEnrichmentJob(ctx, "manifestation", "succeeded")
	o.ObserveEnrichmentJob(ctx, "origin", "failed")

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `enrichment_jobs_processed_total{kind="manifestation",outcome="succeeded"} 1`) {
		t.Errorf("expected a manifestation/succeeded=1 counter, got:\n%s", body)
	}
	if !strings.Contains(body, `enrichment_jobs_processed_total{kind="origin",outcome="failed"} 1`) {
		t.Errorf("expected an origin/failed=1 counter, got:\n%s", body)
	}
}

func TestObserveAnalyticsEvent_RecordsOutcome(t *testing.T) {
	o := newTestObservability(t)
	o.ObserveAnalyticsEvent("written")
	o.ObserveAnalyticsEvent("dropped")
	o.ObserveAnalyticsEvent("dropped")

	body := scrapeMetrics(t, o)
	if !strings.Contains(body, `analytics_events_total{outcome="written"} 1`) {
		t.Errorf("expected a written=1 counter, got:\n%s", body)
	}
	if !strings.Contains(body, `analytics_events_total{outcome="dropped"} 2`) {
		t.Errorf("expected a dropped=2 counter, got:\n%s", body)
	}
}

func scrapeMetrics(t *testing.T, o *Observability) string {
	t.Helper()
	rec := httptest.NewRecorder()
	o.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}
