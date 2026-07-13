-- +goose Up
-- Extends the origin-diffusion cache (see 00003/00004/00005) to also carry
-- a rerun's resolved Title, alongside its mainImage/bodyMarkdown/standfirst.
-- NULL means "not fetched yet", distinct from a fetched-but-empty ''.
ALTER TABLE diffusion_origin_image_cache
    ADD COLUMN title TEXT;

-- +goose Down
ALTER TABLE diffusion_origin_image_cache
    DROP COLUMN title;
