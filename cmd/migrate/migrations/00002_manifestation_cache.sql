-- +goose Up
CREATE TABLE manifestation_cache (
    manifestation_id       TEXT PRIMARY KEY,
    diffusion_id           TEXT NOT NULL,
    show_id                TEXT,
    show_title              TEXT,
    url                    TEXT NOT NULL,
    duration_seconds       INT,
    principal              BOOLEAN NOT NULL DEFAULT false,
    diffusion_updated_time BIGINT NOT NULL DEFAULT 0,
    expires_at             TIMESTAMPTZ,
    fetched_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE manifestation_cache;
