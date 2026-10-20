package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestOpenRunsMigrations(t *testing.T) {
	store, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	sqlDB, err := store.SQLDB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='repos'").Scan(&count); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected repos table to exist")
	}
}

func TestSaveSnapshotTransactionRollback(t *testing.T) {
	store, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	err = store.SaveSnapshot(Snapshot{
		Repos: []RepoSnapshot{
			{
				Name:     "acme/repo",
				CloneURL: "git@example.com:acme/repo.git",
				Driver:   "gh",
				Contributors: map[string]ContributorSnapshot{
					"email:alice@example.com": {
						Name:      "Alice",
						Email:     "alice@example.com",
						FirstSeen: time.Now(),
						LastSeen:  time.Now(),
						Commits:   -1, // violates CHECK(commits >= 0)
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected save snapshot error")
	}

	snap, err := store.LoadSnapshot()
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(snap.Repos) != 0 {
		t.Fatalf("expected rollback to keep repos empty")
	}
}

func TestInsertCommitIfNewIdempotentAndRequiresRepo(t *testing.T) {
	store, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	_, err = store.InsertCommitIfNew(
		RepoRef{Driver: "gh", Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git"},
		CommitInput{SHA: "abc123", FirstSeenAt: time.Now(), Source: "poll"},
	)
	if err == nil {
		t.Fatalf("expected error when repo is missing")
	}

	if err := store.SaveSnapshot(Snapshot{
		Repos: []RepoSnapshot{
			{Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git", Driver: "gh"},
		},
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	inserted, err := store.InsertCommitIfNew(
		RepoRef{Driver: "gh", Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git"},
		CommitInput{SHA: "abc123", FirstSeenAt: time.Now(), Source: "poll"},
	)
	if err != nil {
		t.Fatalf("insert commit: %v", err)
	}
	if !inserted {
		t.Fatalf("expected first insert to be true")
	}

	inserted, err = store.InsertCommitIfNew(
		RepoRef{Driver: "gh", Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git"},
		CommitInput{SHA: "abc123", FirstSeenAt: time.Now(), Source: "poll"},
	)
	if err != nil {
		t.Fatalf("insert duplicate commit: %v", err)
	}
	if inserted {
		t.Fatalf("expected duplicate insert to be false")
	}

	var commitCount int64
	if err := store.DB().Table("commits").Count(&commitCount).Error; err != nil {
		t.Fatalf("count commits: %v", err)
	}
	if commitCount != 1 {
		t.Fatalf("expected one commit row, got %d", commitCount)
	}
}

func TestUpsertChangeRequestCreateThenUpdate(t *testing.T) {
	store, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.SaveSnapshot(Snapshot{
		Repos: []RepoSnapshot{
			{Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git", Driver: "gh"},
		},
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	number := 42
	created, err := store.UpsertChangeRequest(ChangeRequestInput{
		Repo:        RepoRef{Driver: "gh", Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git"},
		Provider:    "github",
		ExternalID:  "pr_42",
		Number:      &number,
		Kind:        "pr",
		Title:       "Initial title",
		State:       "open",
		FirstSeenAt: time.Now(),
		Source:      "webhook",
	})
	if err != nil {
		t.Fatalf("create change request: %v", err)
	}
	if !created {
		t.Fatalf("expected first upsert to create")
	}

	created, err = store.UpsertChangeRequest(ChangeRequestInput{
		Repo:        RepoRef{Driver: "gh", Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git"},
		Provider:    "github",
		ExternalID:  "pr_42",
		Number:      &number,
		Kind:        "pr",
		Title:       "Updated title",
		State:       "closed",
		FirstSeenAt: time.Now(),
		Source:      "webhook",
	})
	if err != nil {
		t.Fatalf("update change request: %v", err)
	}
	if created {
		t.Fatalf("expected second upsert to update existing row")
	}

	var count int64
	if err := store.DB().Table("change_requests").Count(&count).Error; err != nil {
		t.Fatalf("count change requests: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one change request row, got %d", count)
	}
}

func TestRecordWebhookDeliveryIdempotent(t *testing.T) {
	store, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	firstID, created, err := store.RecordWebhookDelivery(WebhookDeliveryInput{
		Provider:   "github",
		DeliveryID: "delivery-1",
		EventType:  "pull_request",
		ReceivedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("record first delivery: %v", err)
	}
	if !created {
		t.Fatalf("expected first delivery to be new")
	}

	secondID, created, err := store.RecordWebhookDelivery(WebhookDeliveryInput{
		Provider:   "github",
		DeliveryID: "delivery-1",
		EventType:  "pull_request",
		ReceivedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("record duplicate delivery: %v", err)
	}
	if created {
		t.Fatalf("expected duplicate delivery to be existing")
	}
	if firstID != secondID {
		t.Fatalf("expected duplicate delivery to return same id")
	}
}

// TestConcurrentWritesDoNotLock guards against "database is locked" when many
// goroutines, each with its own connection pool, run read-then-write
// transactions against the same database file.
func TestConcurrentWritesDoNotLock(t *testing.T) {
	path := t.TempDir() + "/state.db"
	seed, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	ref := RepoRef{Driver: "gh", Name: "acme/repo", CloneURL: "git@example.com:acme/repo.git"}
	if err := seed.UpsertRepo(RepoUpsertInput{Driver: ref.Driver, Name: ref.Name, CloneURL: ref.CloneURL}); err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	const workers = 20
	const perWorker = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		store, err := Open(path)
		if err != nil {
			t.Fatalf("open store %d: %v", w, err)
		}
		wg.Add(1)
		go func(w int, store *Store) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				_, err := store.InsertCommitIfNew(ref, CommitInput{
					SHA:         fmt.Sprintf("%04d%04d", w, i),
					CommittedAt: time.Now(),
					FirstSeenAt: time.Now(),
					Source:      "test",
				})
				if err != nil {
					errs <- err
				}
			}
		}(w, store)
	}
	wg.Wait()
	close(errs)

	failed := 0
	var first error
	for err := range errs {
		if first == nil {
			first = err
		}
		failed++
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent writes failed, first: %v", failed, workers*perWorker, first)
	}
}
