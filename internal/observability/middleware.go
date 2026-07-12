package observability

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Wrap records request-count and latency metrics and emits one structured log
// line per request. route should be a short, low-cardinality name for the
// endpoint (e.g. "rss", "audio") - never the raw request path, which would
// blow up metric cardinality with one series per show/manifestation ID.
func (o *Observability) Wrap(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		h(rec, r)

		duration := time.Since(start)
		status := strconv.Itoa(rec.status)

		o.httpRequests.Add(r.Context(), 1, metric.WithAttributes(
			attribute.String("route", route), attribute.String("method", r.Method), attribute.String("status", status)))
		o.httpRequestDuration.Record(r.Context(), duration.Seconds(), metric.WithAttributes(
			attribute.String("route", route), attribute.String("method", r.Method)))

		slog.InfoContext(r.Context(), "http_request",
			"route", route,
			"method", r.Method,
			"status", rec.status,
			"duration_ms", duration.Milliseconds(),
			"user_agent", r.UserAgent(),
		)
	}
}

// statusRecorder captures the status code written by the wrapped handler,
// since http.ResponseWriter doesn't expose it after the fact.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
