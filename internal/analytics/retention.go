package analytics

import (
	"context"
	"log/slog"
	"time"
)

// RunRetention periodically deletes request_log rows older than maxAge,
// until ctx is done. Meant to be started as a background goroutine.
func (w *Writer) RunRetention(ctx context.Context, maxAge, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.deleteOlderThan(ctx, maxAge)
		}
	}
}

func (w *Writer) deleteOlderThan(ctx context.Context, maxAge time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Pass maxAge as a plain number of seconds rather than relying on
	// Postgres to parse Go's time.Duration string format (e.g.
	// "2160h0m0s"), which isn't a format Postgres's interval parser is
	// guaranteed to accept.
	tag, err := w.pool.Exec(ctx, "DELETE FROM request_log WHERE created_at < now() - ($1 * interval '1 second')", maxAge.Seconds())
	if err != nil {
		slog.Error("analytics retention cleanup failed", "error", err)
		return
	}
	if tag.RowsAffected() > 0 {
		slog.Info("analytics retention cleanup", "rowsDeleted", tag.RowsAffected())
	}
}
