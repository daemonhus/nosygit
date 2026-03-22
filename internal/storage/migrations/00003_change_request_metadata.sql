-- +goose Up
ALTER TABLE change_requests ADD COLUMN description TEXT NULL;
ALTER TABLE change_requests ADD COLUMN source_sha TEXT NULL;
ALTER TABLE change_requests ADD COLUMN comments_count INTEGER NULL;

-- +goose Down
-- SQLite does not support DROP COLUMN in a straightforward portable way here.
