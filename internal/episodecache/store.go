package episodecache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed manifestation cache.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Get returns the cached entry for manifestationID, or ok=false if there is
// no row yet.
func (s *Store) Get(ctx context.Context, manifestationID string) (Entry, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT manifestation_id, diffusion_id, coalesce(show_id, ''), coalesce(show_title, ''),
		       url, coalesce(duration_seconds, 0), principal, diffusion_updated_time, expires_at, fetched_at
		FROM manifestation_cache
		WHERE manifestation_id = $1`, manifestationID)

	var e Entry
	var durationSeconds int
	if err := row.Scan(&e.ManifestationID, &e.DiffusionID, &e.ShowID, &e.ShowTitle,
		&e.URL, &durationSeconds, &e.Principal, &e.DiffusionUpdatedTime, &e.ExpiresAt, &e.FetchedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("querying manifestation_cache: %w", err)
	}
	e.Duration = secondsToDuration(durationSeconds)
	return e, true, nil
}

// Upsert inserts or replaces the cached entry for e.ManifestationID.
func (s *Store) Upsert(ctx context.Context, e Entry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO manifestation_cache
			(manifestation_id, diffusion_id, show_id, show_title, url, duration_seconds, principal, diffusion_updated_time, expires_at, fetched_at)
		VALUES ($1, $2, nullif($3, ''), nullif($4, ''), $5, $6, $7, $8, $9, now())
		ON CONFLICT (manifestation_id) DO UPDATE SET
			diffusion_id = excluded.diffusion_id,
			show_id = excluded.show_id,
			show_title = excluded.show_title,
			url = excluded.url,
			duration_seconds = excluded.duration_seconds,
			principal = excluded.principal,
			diffusion_updated_time = excluded.diffusion_updated_time,
			expires_at = excluded.expires_at,
			fetched_at = excluded.fetched_at`,
		e.ManifestationID, e.DiffusionID, e.ShowID, e.ShowTitle, e.URL,
		durationToSeconds(e.Duration), e.Principal, e.DiffusionUpdatedTime, e.ExpiresAt)
	if err != nil {
		return fmt.Errorf("upserting manifestation_cache: %w", err)
	}
	return nil
}

func durationToSeconds(d time.Duration) int {
	return int(d.Seconds())
}

func secondsToDuration(s int) time.Duration {
	return time.Duration(s) * time.Second
}
