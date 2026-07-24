-- +goose Up
CREATE TABLE feed_cache (
    show_id            TEXT NOT NULL,
    page               INT NOT NULL,
    body               TEXT NOT NULL,
    show_title         TEXT NOT NULL,
    diffusions         JSONB NOT NULL,
    had_degraded       BOOLEAN NOT NULL,
    earliest_expiry    TIMESTAMPTZ,
    first_diffusion_id TEXT NOT NULL,
    page0_anchor_id    TEXT,
    cached_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (show_id, page)
);

CREATE INDEX feed_cache_page0_sweep_idx ON feed_cache (cached_at) WHERE page = 0;

-- +goose Down
DROP TABLE feed_cache;
