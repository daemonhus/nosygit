-- +goose Up
CREATE TABLE IF NOT EXISTS provider_polls (
    provider TEXT PRIMARY KEY,
    last_polled_at DATETIME NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS provider_polls;
