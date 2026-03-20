-- +goose Up
DROP INDEX IF EXISTS idx_repos_provider_repo_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_provider_repo_id ON repos(provider, provider_repo_id) WHERE provider != '' AND provider_repo_id != '';

-- +goose Down
DROP INDEX IF EXISTS idx_repos_provider_repo_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_provider_repo_id ON repos(provider, provider_repo_id);
