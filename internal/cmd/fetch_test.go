package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
)

// newSourceRepo creates an on-disk git repository with a single commit and
// returns its path, suitable for cloning via a local file URL in tests.
func newSourceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init source repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir
}

func TestFetchProvidersSuccess(t *testing.T) {
	src := newSourceRepo(t)
	cfg := config.Config{ClonePath: t.TempDir() + "/"}
	providers := []config.Provider{{Name: "local", Driver: "git", URL: src}}

	state, failed := fetchProviders(cfg, providers, config.State{}, nil)

	if failed != 0 {
		t.Fatalf("expected 0 failed providers, got %d", failed)
	}
	if len(state.Repos) != 1 {
		t.Fatalf("expected cloned repo to be saved in state, got %d repos", len(state.Repos))
	}
}

func TestFetchProvidersCountsFailures(t *testing.T) {
	cfg := config.Config{ClonePath: t.TempDir() + "/"}
	// A nonexistent local path fails to clone offline without hanging.
	providers := []config.Provider{{
		Name:   "broken",
		Driver: "git",
		URL:    filepath.Join(t.TempDir(), "does-not-exist"),
	}}

	_, failed := fetchProviders(cfg, providers, config.State{}, nil)

	if failed != 1 {
		t.Fatalf("expected 1 failed provider, got %d", failed)
	}
}

func TestFetchProvidersUnsupportedDriverNotCountedAsFailure(t *testing.T) {
	cfg := config.Config{ClonePath: t.TempDir() + "/"}
	providers := []config.Provider{{Name: "weird", Driver: "mercurial"}}

	state, failed := fetchProviders(cfg, providers, config.State{}, nil)

	if failed != 0 {
		t.Fatalf("unsupported driver should be skipped, not counted as failure; got %d", failed)
	}
	if len(state.Repos) != 0 {
		t.Fatalf("expected no repos in state, got %d", len(state.Repos))
	}
}

func TestFetchProvidersMixedSuccessAndFailure(t *testing.T) {
	src := newSourceRepo(t)
	cfg := config.Config{ClonePath: t.TempDir() + "/"}
	providers := []config.Provider{
		{Name: "ok", Driver: "git", URL: src},
		{Name: "bad", Driver: "git", URL: filepath.Join(t.TempDir(), "nope")},
	}

	_, failed := fetchProviders(cfg, providers, config.State{}, nil)

	if failed != 1 {
		t.Fatalf("expected exactly 1 failed provider, got %d", failed)
	}
}
