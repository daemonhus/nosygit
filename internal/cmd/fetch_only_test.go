package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"nosygit/internal/watchers"
)

func commitFile(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()}
	hash, err := wt.Commit(msg, &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		t.Fatal(err)
	}
	return hash.String()
}

// TestRunGitDetectsNewCommitsWithoutTouchingWorktree checks that polling picks
// up upstream commits while leaving uncommitted edits and untracked files alone.
func TestRunGitDetectsNewCommitsWithoutTouchingWorktree(t *testing.T) {
	src := newSourceRepo(t)
	clonePath := t.TempDir()
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: config.Config{}})

	oldHead, err := cloneIfNeeded(ctx, "acme/repo", src, clonePath)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	clone := filepath.Join(clonePath, "acme", "repo")

	edited := filepath.Join(clone, "README.md")
	untracked := filepath.Join(clone, "scratch.txt")
	if err := os.WriteFile(edited, []byte("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untracked, []byte("scratch"), 0o644); err != nil {
		t.Fatal(err)
	}

	newHead := commitFile(t, src, "second.txt", "two", "second")

	var persisted int
	deps := runGitDeps{
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			for c := chain; c != nil; c = c.Prev {
				persisted++
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {},
		emitBranch:      func(ctx context.Context, repoState config.RepoState, operation string, branch string) {},
	}
	updated, err := RunGitWithDeps(ctx, clone, config.RepoState{Name: "acme/repo", CloneUrl: src, Head: oldHead}, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps: %v", err)
	}
	if updated.Head != newHead {
		t.Fatalf("head = %s, want %s", updated.Head, newHead)
	}
	if persisted == 0 {
		t.Fatal("expected the new commit to be processed")
	}

	if data, _ := os.ReadFile(edited); string(data) != "local edit" {
		t.Fatalf("uncommitted edit was overwritten: %q", data)
	}
	if data, _ := os.ReadFile(untracked); string(data) != "scratch" {
		t.Fatalf("untracked file was removed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(clone, "second.txt")); !os.IsNotExist(err) {
		t.Fatalf("worktree must not be updated by polling, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, ".git", "HEAD")); err != nil {
		t.Fatalf(".git must be intact: %v", err)
	}
}

func noopGitDeps() runGitDeps {
	return runGitDeps{
		persistAndEmit:  func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {},
		emitBranch:      func(ctx context.Context, repoState config.RepoState, operation string, branch string) {},
	}
}

func cloneForPolling(t *testing.T, src string) (string, string, context.Context) {
	t.Helper()
	clonePath := t.TempDir()
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: config.Config{}})
	head, err := cloneIfNeeded(ctx, "acme/repo", src, clonePath)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	return filepath.Join(clonePath, "acme", "repo"), head, ctx
}

// A clean clone is fast-forwarded properly: branch, files and index all move,
// so the clone does not look dirty afterwards.
func TestRunGitFastForwardsCleanClone(t *testing.T) {
	src := newSourceRepo(t)
	clone, oldHead, ctx := cloneForPolling(t, src)
	newHead := commitFile(t, src, "second.txt", "two", "second")

	updated, err := RunGitWithDeps(ctx, clone, config.RepoState{Name: "acme/repo", CloneUrl: src, Head: oldHead}, noopGitDeps())
	if err != nil {
		t.Fatalf("RunGitWithDeps: %v", err)
	}
	if updated.Head != newHead {
		t.Fatalf("head = %s, want %s", updated.Head, newHead)
	}
	if data, err := os.ReadFile(filepath.Join(clone, "second.txt")); err != nil || string(data) != "two" {
		t.Fatalf("files should be updated for a clean clone: %q, %v", data, err)
	}

	repo, err := git.PlainOpen(clone)
	if err != nil {
		t.Fatal(err)
	}
	if local, _ := repo.Head(); local.Hash().String() != newHead {
		t.Fatalf("local branch = %s, want %s", local.Hash(), newHead)
	}
	wt, _ := repo.Worktree()
	if status, err := wt.Status(); err != nil || !status.IsClean() {
		t.Fatalf("clone should be clean after a poll, got %v (err %v)", status, err)
	}
}

// Local commits that are not on origin are never discarded; origin's commits
// are still detected.
func TestRunGitKeepsLocalCommits(t *testing.T) {
	src := newSourceRepo(t)
	clone, oldHead, ctx := cloneForPolling(t, src)
	localHead := commitFile(t, clone, "local.txt", "mine", "local work")
	newHead := commitFile(t, src, "second.txt", "two", "second")

	updated, err := RunGitWithDeps(ctx, clone, config.RepoState{Name: "acme/repo", CloneUrl: src, Head: oldHead}, noopGitDeps())
	if err != nil {
		t.Fatalf("RunGitWithDeps: %v", err)
	}
	if updated.Head != newHead {
		t.Fatalf("origin's commit should be detected: head = %s, want %s", updated.Head, newHead)
	}
	repo, _ := git.PlainOpen(clone)
	if local, _ := repo.Head(); local.Hash().String() != localHead {
		t.Fatalf("local commit was discarded: HEAD = %s, want %s", local.Hash(), localHead)
	}
	if _, err := os.Stat(filepath.Join(clone, "local.txt")); err != nil {
		t.Fatalf("local file missing: %v", err)
	}
}
