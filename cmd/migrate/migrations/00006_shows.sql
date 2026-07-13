-- +goose Up
CREATE TABLE shows (
    show_id    TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE shows;
