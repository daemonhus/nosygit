package cmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"nosygit/internal/storage"
	"nosygit/internal/watchers"
)

func TestPersistAndEmitNewCommits_EmptyDBThenSeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	repoState := config.RepoState{
		Name:     "acme/repo",
		CloneUrl: "git@example.com:acme/repo.git",
		Driver:   "gh",
	}

	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:   config.Config{Watchers: map[string]config.Watcher{}, Events: map[string]config.Event{}},
		Provider: config.Provider{Name: "gh"},
		Store:    store,
	})

	commit := &object.Commit{
		Hash: plumbing.NewHash("0123456789abcdef0123456789abcdef01234567"),
		Author: object.Signature{
			Name:  "Alice",
			Email: "alice@example.com",
			When:  time.Now().UTC(),
		},
		Committer: object.Signature{
			Name:  "Alice",
			Email: "alice@example.com",
			When:  time.Now().UTC(),
		},
		Message: "feat: first commit\n\nbody",
	}
	chain := watchers.CommitChain{Curr: commit}

	// Empty DB and unknown repo should not insert.
	PersistAndEmitNewCommits(ctx, &chain, repoState)

	var count int64
	if err := store.DB().Table("commits").Count(&count).Error; err != nil {
		t.Fatalf("count commits pre-seed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no commit rows before seeding repo")
	}

	// Seed state so repo exists, then insert should succeed once.
	state := config.State{Repos: []config.RepoState{repoState}}
	if err := state.WriteState(dbPath); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	PersistAndEmitNewCommits(ctx, &chain, repoState)
	if err := store.DB().Table("commits").Count(&count).Error; err != nil {
		t.Fatalf("count commits after seed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one commit row after seed, got %d", count)
	}

	// Duplicate should be idempotent.
	PersistAndEmitNewCommits(ctx, &chain, repoState)
	if err := store.DB().Table("commits").Count(&count).Error; err != nil {
		t.Fatalf("count commits after duplicate: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one commit row after duplicate, got %d", count)
	}
}
