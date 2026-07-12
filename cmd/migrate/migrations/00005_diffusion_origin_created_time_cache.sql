-- +goose Up
-- Adds the origin diffusion's own createdTime (its original broadcast date)
-- to the cache (see 00003/00004), used to flag rerun episodes in the feed.
ALTER TABLE diffusion_origin_image_cache
    ADD COLUMN created_time BIGINT;

-- +goose Down
ALTER TABLE diffusion_origin_image_cache
    DROP COLUMN created_time;
