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
