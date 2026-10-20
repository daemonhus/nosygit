package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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
	if isCorruptRepoErr(errNotGitRepo) {
		t.Fatal("a path that was never a repo is not corruption")
	}
	if isCorruptRepoErr(os.ErrPermission) {
		t.Fatal("permission errors are transient, not corruption")
	}
	if isCorruptRepoErr(errors.New("too many open files")) {
		t.Fatal("fd exhaustion is transient, not corruption")
	}
	if !isCorruptRepoErr(plumbing.ErrObjectNotFound) {
		t.Fatal("missing objects should count as corruption")
	}
}

func safetyCtx() context.Context {
	return context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: config.Config{}})
}

func TestCloneIfNeededQuarantinesCorruptRepo(t *testing.T) {
	src := newSourceRepo(t)
	clonePath := t.TempDir()

	if _, err := cloneIfNeeded(safetyCtx(), "acme/repo", src, clonePath); err != nil {
		t.Fatalf("initial clone: %v", err)
	}
	rootPath := filepath.Join(clonePath, "acme", "repo")
	if err := os.WriteFile(filepath.Join(rootPath, "notes.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(rootPath, ".git", "objects")); err != nil {
		t.Fatal(err)
	}

	head, err := cloneIfNeeded(safetyCtx(), "acme/repo", src, clonePath)
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

func TestCloneIfNeededLeavesNonRepoDirectoryAlone(t *testing.T) {
	src := newSourceRepo(t)
	clonePath := t.TempDir()

	// A group directory that holds other clones is not a repository.
	groupPath := filepath.Join(clonePath, "group", "sub")
	nested := filepath.Join(groupPath, "other", "file.txt")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("other clone"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := cloneIfNeeded(safetyCtx(), "group/sub", src, clonePath); err == nil {
		t.Fatal("expected an error for a non-empty, non-repo directory")
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("non-repo directory contents must be untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clonePath, ".quarantine")); !os.IsNotExist(err) {
		t.Fatalf("nothing should be quarantined, stat err: %v", err)
	}
}

func TestCloneIfNeededClonesIntoEmptyDirectory(t *testing.T) {
	src := newSourceRepo(t)
	clonePath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clonePath, "acme", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	head, err := cloneIfNeeded(safetyCtx(), "acme/repo", src, clonePath)
	if err != nil || head == "" {
		t.Fatalf("expected clone into empty dir, got head %q err %v", head, err)
	}
}

func safetyCtxWithShallow(shallow config.ShallowConfig) context.Context {
	return context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: config.Config{Shallow: shallow}})
}
