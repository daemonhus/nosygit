-- +goose Up
CREATE TABLE IF NOT EXISTS state_meta (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_run DATETIME NULL
);

CREATE TABLE IF NOT EXISTS repos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider TEXT NULL,
    provider_repo_id TEXT NULL,
    name TEXT NOT NULL,
    clone_url TEXT NOT NULL,
    public INTEGER NOT NULL,
    head TEXT NOT NULL DEFAULT '',
    driver TEXT NOT NULL,
    default_branch TEXT NULL,
    archived INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_provider_repo_id ON repos(provider, provider_repo_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_driver_name ON repos(driver, name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_driver_clone_url ON repos(driver, clone_url);
CREATE INDEX IF NOT EXISTS idx_repos_driver ON repos(driver);

CREATE TABLE IF NOT EXISTS repo_contributors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL,
    identity_key TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    first_seen DATETIME NOT NULL,
    last_seen DATETIME NOT NULL,
    commits INTEGER NOT NULL DEFAULT 0 CHECK (commits >= 0),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, identity_key),
    FOREIGN KEY (repo_id) REFERENCES repos(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_repo_contributors_repo_id ON repo_contributors(repo_id);

CREATE TABLE IF NOT EXISTS commits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL,
    sha TEXT NOT NULL,
    author_name TEXT NULL,
    author_email TEXT NULL,
    committer_name TEXT NULL,
    committer_email TEXT NULL,
    committed_at DATETIME NULL,
    message_title TEXT NULL,
    message TEXT NULL,
    first_seen_at DATETIME NOT NULL,
    source TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(repo_id, sha),
    FOREIGN KEY (repo_id) REFERENCES repos(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_commits_repo_id ON commits(repo_id);
CREATE INDEX IF NOT EXISTS idx_commits_repo_first_seen ON commits(repo_id, first_seen_at);

CREATE TABLE IF NOT EXISTS change_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL,
    provider TEXT NOT NULL,
    external_id TEXT NOT NULL,
    number INTEGER NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    author_login TEXT NULL,
    author_id TEXT NULL,
    source_branch TEXT NULL,
    target_branch TEXT NULL,
    state TEXT NOT NULL,
    is_draft INTEGER NOT NULL DEFAULT 0,
    opened_at DATETIME NULL,
    closed_at DATETIME NULL,
    merged_at DATETIME NULL,
    updated_at_provider DATETIME NULL,
    first_seen_at DATETIME NOT NULL,
    source TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(provider, external_id),
    FOREIGN KEY (repo_id) REFERENCES repos(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_change_requests_repo_id ON change_requests(repo_id);
CREATE INDEX IF NOT EXISTS idx_change_requests_state ON change_requests(state);

CREATE TABLE IF NOT EXISTS change_request_commits (
    change_request_id INTEGER NOT NULL,
    commit_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (change_request_id, commit_id),
    FOREIGN KEY (change_request_id) REFERENCES change_requests(id) ON DELETE CASCADE,
    FOREIGN KEY (commit_id) REFERENCES commits(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    received_at DATETIME NOT NULL,
    repo_id INTEGER NULL,
    payload_json TEXT NULL,
    processed_at DATETIME NULL,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(provider, delivery_id),
    FOREIGN KEY (repo_id) REFERENCES repos(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_received_at ON webhook_deliveries(received_at);

-- +goose Down
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS change_request_commits;
DROP TABLE IF EXISTS change_requests;
DROP TABLE IF EXISTS commits;
DROP TABLE IF EXISTS repo_contributors;
DROP TABLE IF EXISTS repos;
DROP TABLE IF EXISTS state_meta;
