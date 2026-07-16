// Package shows keeps the Postgres "shows" table (show_id -> title) current
// as requests reveal a show, so Grafana panels that join show_id-labeled
// metrics/logs against a friendly display name have something to join
// against - see observability.Observability.ObserveShowRequest (the
// show_feed_requests_total Prometheus metric) and the "Top shows by RSS
// requests" panel on the nginx Grafana dashboard, which does the same join
// against the nginx access log instead.
//
// Upserts are asynchronous and best-effort: a request never waits on the
// database, and under sustained overload an upsert is dropped (and logged)
// rather than blocking.
package shows

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// update is one show_id/title pair queued for an async upsert.
type update struct {
	showID string
	title  string
}

// Registry batches show upserts into Postgres via a bounded channel and a
// small worker pool.
type Registry struct {
	pool    *pgxpool.Pool
	updates chan update
}

// NewRegistry creates a Registry backed by pool. bufferSize bounds how many
// updates can be queued before new ones are dropped; workers is how many
// concurrent upserts run.
func NewRegistry(pool *pgxpool.Pool, bufferSize, workers int) *Registry {
	r := &Registry{
		pool:    pool,
		updates: make(chan update, bufferSize),
	}
	for i := 0; i < workers; i++ {
		go r.worker()
	}
	return r
}

func (r *Registry) worker() {
	for u := range r.updates {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := r.pool.Exec(ctx, `
			INSERT INTO shows (show_id, title, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (show_id) DO UPDATE SET title = excluded.title, updated_at = excluded.updated_at`,
			u.showID, u.title)
		cancel()
		if err != nil {
			slog.Error("show upsert failed", "error", err)
		}
	}
}

// Record queues showID/title for an async upsert into the shows table. A
// no-op if either is empty (e.g. a show not yet resolved).
func (r *Registry) Record(ctx context.Context, showID, title string) {
	if showID == "" || title == "" {
		return
	}
	select {
	case r.updates <- update{showID: showID, title: title}:
	default:
		slog.Warn("show upsert dropped: buffer full", "showID", showID)
	}
}
