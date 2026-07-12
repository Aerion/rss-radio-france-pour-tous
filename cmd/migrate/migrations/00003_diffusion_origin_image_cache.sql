-- +goose Up
CREATE TABLE diffusion_origin_image_cache (
    diffusion_id TEXT PRIMARY KEY,
    main_image   TEXT NOT NULL DEFAULT '',
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE diffusion_origin_image_cache;
