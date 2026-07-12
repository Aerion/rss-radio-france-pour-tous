-- +goose Up
-- Extends the origin-diffusion cache (see 00003) to also carry a rerun's
-- resolved bodyMarkdown/standfirst, alongside its mainImage. NULL means
-- "not fetched yet" for that pair, distinct from a fetched-but-empty ''.
ALTER TABLE diffusion_origin_image_cache
    ADD COLUMN body_markdown TEXT,
    ADD COLUMN standfirst TEXT;

-- +goose Down
ALTER TABLE diffusion_origin_image_cache
    DROP COLUMN body_markdown,
    DROP COLUMN standfirst;
