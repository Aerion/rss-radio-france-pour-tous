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

	radioFranceRequests metric.Int64Counter
	radioFranceDuration metric.Float64Histogram
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

	return &Observability{
		promRegistry:        promRegistry,
		meterProvider:       meterProvider,
		httpRequests:        httpRequests,
		httpRequestDuration: httpRequestDuration,
		radioFranceRequests: radioFranceRequests,
		radioFranceDuration: radioFranceDuration,
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
