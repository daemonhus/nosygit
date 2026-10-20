package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"nosygit/internal/config"
)

func TestOpenRepoPathRejectsNonRepositories(t *testing.T) {
	empty := t.TempDir()
	if _, _, err := OpenRepoPath(empty); !errors.Is(err, errNotGitRepo) {
		t.Fatalf("empty dir: got %v, want errNotGitRepo", err)
	}

	gitFile := t.TempDir()
	if err := os.WriteFile(filepath.Join(gitFile, ".git"), []byte("gitdir: elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenRepoPath(gitFile); !errors.Is(err, errNotGitRepo) {
		t.Fatalf(".git file: got %v, want errNotGitRepo", err)
	}
}

func TestOpenRepoPathOpensWorkingTreeAndBare(t *testing.T) {
	src := newSourceRepo(t)
	repo, s, err := OpenRepoPath(src)
	if err != nil {
		t.Fatalf("working tree: %v", err)
	}
	s.Close()
	if _, err := repo.Worktree(); err != nil {
		t.Fatalf("working tree should have a worktree: %v", err)
	}

	bare := t.TempDir()
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	repo, s, err = OpenRepoPath(bare)
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	s.Close()
	if _, err := repo.Worktree(); !errors.Is(err, git.ErrIsBareRepository) {
		t.Fatalf("bare repo must have no worktree, got %v", err)
	}
}

func TestIsCorruptRepoErr(t *testing.T) {
	if !isCorruptRepoErr(errNotGitRepo) {
		t.Fatal("errNotGitRepo should count as corruption")
	}
	if isCorruptRepoErr(os.ErrPermission) {
		t.Fatal("permission errors are transient, not corruption")
	}
	if isCorruptRepoErr(errors.New("too many open files")) {
		t.Fatal("fd exhaustion is transient, not corruption")
	}
}

func TestCloneIfNeededQuarantinesInsteadOfDeleting(t *testing.T) {
	src := newSourceRepo(t)
	clonePath := t.TempDir()

	// A directory that exists but is not a repository, holding a local file.
	rootPath := filepath.Join(clonePath, "acme", "repo")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(rootPath, "notes.txt")
	if err := os.WriteFile(precious, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: config.Config{}})
	head, err := cloneIfNeeded(ctx, "acme/repo", src, clonePath)
	if err != nil {
		t.Fatalf("cloneIfNeeded: %v", err)
	}
	if head == "" {
		t.Fatal("expected a head hash from the fresh clone")
	}

	matches, err := filepath.Glob(filepath.Join(clonePath, ".quarantine", "acme__repo-*", "notes.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected quarantined notes.txt, got %v (err %v)", matches, err)
	}
	if data, _ := os.ReadFile(matches[0]); string(data) != "keep me" {
		t.Fatalf("quarantined file content changed: %q", data)
	}
}
