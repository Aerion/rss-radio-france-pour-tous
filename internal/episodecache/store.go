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

// GetOriginImage returns the cached mainImage UUID for a rerun's origin
// diffusion, or ok=false if it hasn't been fetched yet. mainImage may be ""
// with ok=true, meaning the origin diffusion was already checked and simply
// has no mainImage of its own.
func (s *Store) GetOriginImage(ctx context.Context, diffusionID string) (mainImage string, ok bool, err error) {
	row := s.pool.QueryRow(ctx, `
		SELECT main_image FROM diffusion_origin_image_cache WHERE diffusion_id = $1`, diffusionID)

	if err := row.Scan(&mainImage); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("querying diffusion_origin_image_cache: %w", err)
	}
	return mainImage, true, nil
}

// UpsertOriginImage caches mainImage (possibly "") as the resolved image for
// origin diffusion diffusionID.
func (s *Store) UpsertOriginImage(ctx context.Context, diffusionID, mainImage string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO diffusion_origin_image_cache (diffusion_id, main_image, fetched_at)
		VALUES ($1, $2, now())
		ON CONFLICT (diffusion_id) DO UPDATE SET
			main_image = excluded.main_image,
			fetched_at = excluded.fetched_at`,
		diffusionID, mainImage)
	if err != nil {
		return fmt.Errorf("upserting diffusion_origin_image_cache: %w", err)
	}
	return nil
}

// GetOriginBody returns the cached bodyMarkdown/standfirst/createdTime for a
// rerun's origin diffusion, or ok=false if that trio hasn't been fetched yet
// (even if the row already exists because its mainImage was cached
// separately - see the NULL columns added in migrations 00004/00005).
func (s *Store) GetOriginBody(ctx context.Context, diffusionID string) (bodyMarkdown, standfirst string, createdTime int64, ok bool, err error) {
	row := s.pool.QueryRow(ctx, `
		SELECT body_markdown, standfirst, created_time FROM diffusion_origin_image_cache WHERE diffusion_id = $1`, diffusionID)

	var body, sf *string
	var ct *int64
	if err := row.Scan(&body, &sf, &ct); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", 0, false, nil
		}
		return "", "", 0, false, fmt.Errorf("querying diffusion_origin_image_cache: %w", err)
	}
	if body == nil {
		return "", "", 0, false, nil
	}
	if ct != nil {
		createdTime = *ct
	}
	return *body, *sf, createdTime, true, nil
}

// UpsertOriginBody caches bodyMarkdown/standfirst (either may be "") and
// createdTime as the resolved description fields for origin diffusion
// diffusionID, without disturbing any mainImage already cached for the same
// row.
func (s *Store) UpsertOriginBody(ctx context.Context, diffusionID, bodyMarkdown, standfirst string, createdTime int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO diffusion_origin_image_cache (diffusion_id, body_markdown, standfirst, created_time, fetched_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (diffusion_id) DO UPDATE SET
			body_markdown = excluded.body_markdown,
			standfirst = excluded.standfirst,
			created_time = excluded.created_time,
			fetched_at = excluded.fetched_at`,
		diffusionID, bodyMarkdown, standfirst, createdTime)
	if err != nil {
		return fmt.Errorf("upserting diffusion_origin_image_cache: %w", err)
	}
	return nil
}

func durationToSeconds(d time.Duration) int {
	return int(d.Seconds())
}

func secondsToDuration(s int) time.Duration {
	return time.Duration(s) * time.Second
}
