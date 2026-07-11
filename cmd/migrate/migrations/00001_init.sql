-- +goose Up
CREATE TABLE request_log (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    route      TEXT NOT NULL,
    show_id    TEXT,
    show_title TEXT,
    method     TEXT NOT NULL,
    status     INT NOT NULL,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX request_log_show_id_created_at_idx ON request_log (show_id, created_at);
CREATE INDEX request_log_created_at_idx ON request_log (created_at);

-- +goose Down
DROP TABLE request_log;
