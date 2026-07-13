// Package analytics logs each request into Postgres for usage dashboards
// (requests per show, user-agent breakdown). Writes are asynchronous and
// best-effort: a request never waits on the database, and under sustained
// overload events are dropped (and counted, see MetricsRecorder) rather
// than blocking.
package analytics

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MetricsRecorder receives the outcome of each analytics event, for
// pipeline-health monitoring (is analytics data actually being captured,
// or silently dropped under load). Defined here rather than in a metrics
// package so this package stays decoupled from any particular backend;
// observability.Observability implements it.
type MetricsRecorder interface {
	ObserveAnalyticsEvent(outcome string)
}

const (
	outcomeWritten = "written"
	outcomeDropped = "dropped"
	outcomeFailed  = "failed"
)

// event is one request's worth of analytics data, queued for async insert.
type event struct {
	route     string
	showID    string
	showTitle string
	method    string
	status    int
	userAgent string
}

// Writer batches request events into Postgres via a bounded channel and a
// small worker pool, and doubles as request middleware (see Wrap).
type Writer struct {
	pool    *pgxpool.Pool
	events  chan event
	metrics MetricsRecorder
}

// NewWriter creates a Writer backed by pool. metrics may be nil to skip
// pipeline-health recording. bufferSize bounds how many events can be
// queued before new ones are dropped; workers is how many concurrent
// inserts run.
func NewWriter(pool *pgxpool.Pool, metrics MetricsRecorder, bufferSize, workers int) *Writer {
	w := &Writer{
		pool:    pool,
		events:  make(chan event, bufferSize),
		metrics: metrics,
	}
	for i := 0; i < workers; i++ {
		go w.worker()
	}
	return w
}

func (w *Writer) worker() {
	for e := range w.events {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		if e.showID != "" && e.showTitle != "" {
			w.upsertShow(ctx, e.showID, e.showTitle)
		}

		_, err := w.pool.Exec(ctx, `
			INSERT INTO request_log (route, show_id, show_title, method, status, user_agent)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			e.route, nullIfEmpty(e.showID), nullIfEmpty(e.showTitle), e.method, e.status, e.userAgent)
		cancel()

		if err != nil {
			slog.Error("analytics insert failed", "error", err)
			w.observe(outcomeFailed)
			continue
		}
		w.observe(outcomeWritten)
	}
}

// upsertShow keeps the shows table (show_id -> title) current, so it can be
// joined against show_id-labeled Prometheus metrics (see
// observability.Observability.ObserveShowRequest) or used from Grafana's
// Postgres datasource, without duplicating the title on every request_log
// row. Best-effort: a failure here doesn't affect the request_log write.
func (w *Writer) upsertShow(ctx context.Context, showID, title string) {
	_, err := w.pool.Exec(ctx, `
		INSERT INTO shows (show_id, title, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (show_id) DO UPDATE SET title = excluded.title, updated_at = excluded.updated_at`,
		showID, title)
	if err != nil {
		slog.Error("shows upsert failed", "error", err)
	}
}

func (w *Writer) observe(outcome string) {
	if w.metrics != nil {
		w.metrics.ObserveAnalyticsEvent(outcome)
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Wrap records one analytics event per request. route should be the same
// short semantic name used elsewhere (e.g. "rss", "audio").
func (w *Writer) Wrap(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		ctx, fields := newContext(r.Context())
		r = r.WithContext(ctx)

		rec := &statusRecorder{ResponseWriter: rw, status: http.StatusOK}
		h(rec, r)

		showID, showTitle := fields.snapshot()
		e := event{
			route:     route,
			showID:    showID,
			showTitle: showTitle,
			method:    r.Method,
			status:    rec.status,
			userAgent: r.UserAgent(),
		}

		select {
		case w.events <- e:
		default:
			slog.Warn("analytics event dropped: buffer full", "route", route)
			w.observe(outcomeDropped)
		}
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
