// Package observability sets up OpenTelemetry metrics for the HTTP server and
// outbound Radio France API calls, plus structured per-request logging.
//
// Metrics are served directly at /metrics in Prometheus exposition format
// via the OTel Prometheus exporter (no separate OTel Collector needed).
package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// secondsBuckets mirrors prometheus.DefBuckets, in seconds - the OTel SDK's
// own default histogram boundaries are calibrated for millisecond-ish
// values and would bucket almost every observation into the same bucket
// here, since our durations are recorded in seconds.
var secondsBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// Observability holds this app's metric instruments and doubles as the request
// middleware (see Wrap), so every route gets metrics and logging from one pass.
type Observability struct {
	promRegistry *prometheus.Registry

	meterProvider *sdkmetric.MeterProvider

	httpRequests        metric.Int64Counter
	httpRequestDuration metric.Float64Histogram

	radioFranceRequestsStarted metric.Int64Counter
	radioFranceRequests        metric.Int64Counter
	radioFranceDuration        metric.Float64Histogram

	analyticsEvents metric.Int64Counter

	manifestationCacheLookups metric.Int64Counter

	feedCacheLookups metric.Int64Counter
	feedCacheEntries metric.Int64UpDownCounter

	enrichmentQueueEnqueued metric.Int64Counter
	enrichmentQueueDepth    metric.Int64UpDownCounter
	enrichmentJobsProcessed metric.Int64Counter
}

// New sets up the Prometheus metric provider for serviceName.
func New(serviceName string) (*Observability, error) {
	res := resource.NewSchemaless(attribute.String("service.name", serviceName))

	promRegistry := prometheus.NewRegistry()
	promRegistry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	// WithoutScopeInfo: we only ever use one meter/instrumentation scope
	// for this whole app, so per-scope labels on every metric point would
	// just be noise in queries/dashboards.
	promExporter, err := otelprom.New(otelprom.WithRegisterer(promRegistry), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, fmt.Errorf("creating prometheus exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)

	meter := meterProvider.Meter(serviceName)
	httpRequests, err := meter.Int64Counter("http_requests_total",
		metric.WithDescription("Total HTTP requests handled, labeled by route, method, and status."))
	if err != nil {
		return nil, err
	}
	httpRequestDuration, err := meter.Float64Histogram("http_request_duration_seconds",
		metric.WithDescription("HTTP request latency in seconds, labeled by route and method."),
		metric.WithExplicitBucketBoundaries(secondsBuckets...))
	if err != nil {
		return nil, err
	}
	radioFranceRequestsStarted, err := meter.Int64Counter("radiofrance_api_requests_started_total",
		metric.WithDescription("Outbound calls to the Radio France API dispatched, labeled by logical endpoint - recorded before the call completes, so it can be compared against radiofrance_api_requests_total to spot calls that are hanging."))
	if err != nil {
		return nil, err
	}
	radioFranceRequests, err := meter.Int64Counter("radiofrance_api_requests_total",
		metric.WithDescription("Total outbound calls to the Radio France API, labeled by logical endpoint and outcome."))
	if err != nil {
		return nil, err
	}
	radioFranceDuration, err := meter.Float64Histogram("radiofrance_api_request_duration_seconds",
		metric.WithDescription("Radio France API call latency in seconds, labeled by logical endpoint."),
		metric.WithExplicitBucketBoundaries(secondsBuckets...))
	if err != nil {
		return nil, err
	}
	analyticsEvents, err := meter.Int64Counter("analytics_events_total",
		metric.WithDescription("Outcome of each analytics request-log event, labeled by outcome (written/dropped/failed) - tells you whether usage dashboards can be trusted."))
	if err != nil {
		return nil, err
	}
	manifestationCacheLookups, err := meter.Int64Counter("manifestation_cache_lookups_total",
		metric.WithDescription("Manifestation cache lookups, labeled by outcome (hit/miss) - how often we avoid an extra Radio France API call."))
	if err != nil {
		return nil, err
	}
	feedCacheLookups, err := meter.Int64Counter("feed_cache_lookups_total",
		metric.WithDescription("Rendered-feed cache lookups, labeled by outcome (hit/miss)."))
	if err != nil {
		return nil, err
	}
	feedCacheEntries, err := meter.Int64UpDownCounter("feed_cache_entries",
		metric.WithDescription("Current number of rendered feed pages held in the feed cache."))
	if err != nil {
		return nil, err
	}
	enrichmentQueueEnqueued, err := meter.Int64Counter("enrichment_queue_enqueued_total",
		metric.WithDescription("Episode enrichment jobs offered to the queue, labeled by outcome (queued/duplicate/dropped)."))
	if err != nil {
		return nil, err
	}
	enrichmentQueueDepth, err := meter.Int64UpDownCounter("enrichment_queue_depth",
		metric.WithDescription("Current number of episode enrichment jobs waiting in the queue."))
	if err != nil {
		return nil, err
	}
	enrichmentJobsProcessed, err := meter.Int64Counter("enrichment_jobs_processed_total",
		metric.WithDescription("Episode enrichment jobs processed by a worker, labeled by kind (manifestation/origin) and outcome (succeeded/failed)."))
	if err != nil {
		return nil, err
	}

	return &Observability{
		promRegistry:               promRegistry,
		meterProvider:              meterProvider,
		httpRequests:               httpRequests,
		httpRequestDuration:        httpRequestDuration,
		radioFranceRequestsStarted: radioFranceRequestsStarted,
		radioFranceRequests:        radioFranceRequests,
		radioFranceDuration:        radioFranceDuration,
		analyticsEvents:            analyticsEvents,
		manifestationCacheLookups:  manifestationCacheLookups,
		feedCacheLookups:           feedCacheLookups,
		feedCacheEntries:           feedCacheEntries,
		enrichmentQueueEnqueued:    enrichmentQueueEnqueued,
		enrichmentQueueDepth:       enrichmentQueueDepth,
		enrichmentJobsProcessed:    enrichmentJobsProcessed,
	}, nil
}

// Handler serves this app's metrics in the Prometheus exposition format.
func (o *Observability) Handler() http.Handler {
	return promhttp.HandlerFor(o.promRegistry, promhttp.HandlerOpts{})
}

// Shutdown flushes pending metrics and releases provider resources. Call during
// graceful shutdown.
func (o *Observability) Shutdown(ctx context.Context) error {
	if err := o.meterProvider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down meter provider: %w", err)
	}
	return nil
}

// ObserveRequestStarted implements radiofrance.RequestObserver.
func (o *Observability) ObserveRequestStarted(ctx context.Context, endpoint string) {
	o.radioFranceRequestsStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("endpoint", endpoint)))
}

// ObserveRequest implements radiofrance.RequestObserver.
func (o *Observability) ObserveRequest(ctx context.Context, endpoint string, ok bool, duration time.Duration) {
	status := "ok"
	if !ok {
		status = "error"
	}
	o.radioFranceRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("endpoint", endpoint), attribute.String("status", status)))
	o.radioFranceDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("endpoint", endpoint)))
}

// ObserveAnalyticsEvent implements analytics.MetricsRecorder.
func (o *Observability) ObserveAnalyticsEvent(outcome string) {
	o.analyticsEvents.Add(context.Background(), 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// ObserveCacheLookup implements episodecache.CacheObserver.
func (o *Observability) ObserveCacheLookup(ctx context.Context, outcome string) {
	o.manifestationCacheLookups.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// ObserveFeedCacheLookup implements feedcache.Observer.
func (o *Observability) ObserveFeedCacheLookup(ctx context.Context, outcome string) {
	o.feedCacheLookups.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// AdjustFeedCacheEntries implements feedcache.Observer. delta is positive
// when an entry is added and negative when one is evicted/invalidated.
func (o *Observability) AdjustFeedCacheEntries(ctx context.Context, delta int64) {
	o.feedCacheEntries.Add(ctx, delta)
}

// ObserveEnrichmentEnqueued implements episodecache.EnrichmentObserver.
func (o *Observability) ObserveEnrichmentEnqueued(ctx context.Context, outcome string) {
	o.enrichmentQueueEnqueued.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// AdjustEnrichmentQueueDepth implements episodecache.EnrichmentObserver.
// delta is positive when a job is queued and negative once a worker
// finishes it.
func (o *Observability) AdjustEnrichmentQueueDepth(ctx context.Context, delta int64) {
	o.enrichmentQueueDepth.Add(ctx, delta)
}

// ObserveEnrichmentJob implements episodecache.EnrichmentObserver.
func (o *Observability) ObserveEnrichmentJob(ctx context.Context, kind, outcome string) {
	o.enrichmentJobsProcessed.Add(ctx, 1, metric.WithAttributes(
		attribute.String("kind", kind), attribute.String("outcome", outcome)))
}
