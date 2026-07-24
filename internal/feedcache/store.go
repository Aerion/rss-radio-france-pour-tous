package feedcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed feed cache storage.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Get returns the cached row for showID's page, or ok=false if there is no
// row yet.
func (s *Store) Get(ctx context.Context, showID string, page int) (Row, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT body, show_title, diffusions, had_degraded, earliest_expiry,
		       first_diffusion_id, page0_anchor_id, cached_at
		FROM feed_cache
		WHERE show_id = $1 AND page = $2`, showID, page)

	var r Row
	var diffusionsJSON []byte
	if err := row.Scan(&r.Body, &r.ShowTitle, &diffusionsJSON, &r.HadDegraded, &r.EarliestExpiry,
		&r.FirstDiffusionID, &r.Page0AnchorID, &r.CachedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Row{}, false, nil
		}
		return Row{}, false, fmt.Errorf("querying feed_cache: %w", err)
	}
	if err := json.Unmarshal(diffusionsJSON, &r.Diffusions); err != nil {
		return Row{}, false, fmt.Errorf("unmarshaling feed_cache.diffusions: %w", err)
	}
	r.ShowID = showID
	return r, true, nil
}

// Set inserts or replaces the cached row for showID's page.
func (s *Store) Set(ctx context.Context, showID string, page int, r Row) error {
	diffusionsJSON, err := json.Marshal(r.Diffusions)
	if err != nil {
		return fmt.Errorf("marshaling feed_cache.diffusions: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO feed_cache
			(show_id, page, body, show_title, diffusions, had_degraded, earliest_expiry,
			 first_diffusion_id, page0_anchor_id, cached_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (show_id, page) DO UPDATE SET
			body               = excluded.body,
			show_title         = excluded.show_title,
			diffusions         = excluded.diffusions,
			had_degraded       = excluded.had_degraded,
			earliest_expiry    = excluded.earliest_expiry,
			first_diffusion_id = excluded.first_diffusion_id,
			page0_anchor_id    = excluded.page0_anchor_id,
			cached_at          = excluded.cached_at`,
		showID, page, r.Body, r.ShowTitle, diffusionsJSON, r.HadDegraded, r.EarliestExpiry,
		r.FirstDiffusionID, r.Page0AnchorID)
	if err != nil {
		return fmt.Errorf("upserting feed_cache: %w", err)
	}
	return nil
}

// Delete removes showID's page row, if any.
func (s *Store) Delete(ctx context.Context, showID string, page int) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM feed_cache WHERE show_id = $1 AND page = $2`, showID, page); err != nil {
		return fmt.Errorf("deleting feed_cache row: %w", err)
	}
	return nil
}

// DeleteStalePage0 removes every page-0 row cached at or before olderThan,
// returning how many rows were removed. Only page 0 is swept this way -
// deeper pages are invalidated by Cache.Get's anchor check instead of a
// wall-clock TTL, see the package doc comment.
func (s *Store) DeleteStalePage0(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM feed_cache WHERE page = 0 AND cached_at <= $1`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("sweeping feed_cache: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Count returns the total number of rows currently in the feed cache.
func (s *Store) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM feed_cache`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting feed_cache: %w", err)
	}
	return n, nil
}
