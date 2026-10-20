package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"

	"nosygit/internal/config"
)

func TestProviderClonePathSeparatesProviders(t *testing.T) {
	cfg := config.Config{ClonePath: "/data/clones"}
	gh := providerClonePath(cfg, config.Provider{Name: "github-acme"})
	gl := providerClonePath(cfg, config.Provider{Name: "gitlab-acme"})

	if gh == gl {
		t.Fatalf("providers must not share a clone path: %s", gh)
	}
	if want := "/data/clones/github-acme/"; gh != want {
		t.Fatalf("path = %q, want %q", gh, want)
	}
	// The same owner/repo name from two providers lands in different places.
	if filepath.Join(gh, "acme/infra") == filepath.Join(gl, "acme/infra") {
		t.Fatal("same repo name from different providers must not collide")
	}
}

func TestProviderClonePathDefaultsAndSanitises(t *testing.T) {
	if got := providerClonePath(config.Config{}, config.Provider{Name: "p"}); got != "/tmp/nosygit/p/" {
		t.Fatalf("default path = %q", got)
	}
	for _, name := range []string{"..", "../escape", "a/b", ""} {
		got := providerClonePath(config.Config{ClonePath: "/data"}, config.Provider{Name: name})
		if filepath.Dir(filepath.Clean(got)) != "/data" {
			t.Fatalf("provider name %q escaped the clone path: %q", name, got)
		}
	}
}

func TestIsLegacyDuplicate(t *testing.T) {
	state := config.State{Repos: []config.RepoState{
		{Name: "gitlab-acme/team/app", Driver: "gitlab-acme"},
		{Name: "team/app", Driver: "gitlab-acme"},
		{Name: "gitlab-acme/only-old", Driver: "gitlab-acme"},
		{Name: "team/app", Driver: "github-acme"},
	}}

	if !isLegacyDuplicate(state, state.Repos[0], "") {
		t.Fatal("prefixed entry with an unprefixed counterpart should be a duplicate")
	}
	if isLegacyDuplicate(state, state.Repos[1], "") {
		t.Fatal("the unprefixed entry is the live one")
	}
	if isLegacyDuplicate(state, state.Repos[2], "") {
		t.Fatal("a prefixed entry with no counterpart must still be processed")
	}
	if isLegacyDuplicate(state, state.Repos[3], "") {
		t.Fatal("the same name under another provider is not a duplicate")
	}
}

func TestIsLegacyDuplicateAccountsForDroppedOwner(t *testing.T) {
	state := config.State{Repos: []config.RepoState{
		{Name: "gl/team/app", Driver: "gl"},
		{Name: "acme/team/app", Driver: "gl"},
		{Name: "gl/solo", Driver: "gl"},
	}}
	if !isLegacyDuplicate(state, state.Repos[0], "acme") {
		t.Fatal("legacy name without the owner should match owner/path")
	}
	if isLegacyDuplicate(state, state.Repos[0], "") {
		t.Fatal("without an owner there is no counterpart to match")
	}
	if isLegacyDuplicate(state, state.Repos[2], "acme") {
		t.Fatal("an entry without a counterpart must still be processed")
	}
}

func TestStateClonePathKeepsLegacyNamesWhereTheyAre(t *testing.T) {
	cfg := config.Config{ClonePath: "/data/clones"}
	provider := config.Provider{Name: "gl"}

	if got := stateClonePath(cfg, provider, config.RepoState{Name: "gl/team/app"}); got != "/data/clones/" {
		t.Fatalf("legacy-named entry path = %q", got)
	}
	if got := stateClonePath(cfg, provider, config.RepoState{Name: "acme/team/app"}); got != "/data/clones/gl/" {
		t.Fatalf("current entry path = %q", got)
	}
}

// legacyClone clones src into <base>/<provider>/<path-without-owner>, where the
// older layout kept it, and returns the provider directory.
func legacyClone(t *testing.T, src, provider, path string) string {
	t.Helper()
	providerDir := filepath.Join(t.TempDir(), provider)
	if _, err := git.PlainClone(filepath.Join(providerDir, path), false, &git.CloneOptions{URL: src}); err != nil {
		t.Fatalf("legacy clone: %v", err)
	}
	return providerDir
}

func TestMigrateLegacyCloneMovesMatchingRepo(t *testing.T) {
	src := newSourceRepo(t)
	providerDir := legacyClone(t, src, "gl", "team/app")
	marker := filepath.Join(providerDir, "team", "app", "local.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	moved, err := migrateLegacyClone(providerDir, "acme/team/app", "acme", src)
	if err != nil || !moved {
		t.Fatalf("expected a move, got moved=%v err=%v", moved, err)
	}
	if data, _ := os.ReadFile(filepath.Join(providerDir, "acme", "team", "app", "local.txt")); string(data) != "keep" {
		t.Fatal("files should come along with the move")
	}
	if _, err := os.Stat(filepath.Join(providerDir, "team", "app")); !os.IsNotExist(err) {
		t.Fatalf("old location should be gone: %v", err)
	}
}

func TestMigrateLegacyCloneRefusesUnsafeMoves(t *testing.T) {
	src := newSourceRepo(t)
	other := newSourceRepo(t)

	t.Run("origin differs", func(t *testing.T) {
		providerDir := legacyClone(t, src, "gl", "team/app")
		if moved, _ := migrateLegacyClone(providerDir, "acme/team/app", "acme", other); moved {
			t.Fatal("a repo with a different origin must not be moved")
		}
	})

	t.Run("target exists", func(t *testing.T) {
		providerDir := legacyClone(t, src, "gl", "team/app")
		if err := os.MkdirAll(filepath.Join(providerDir, "acme", "team", "app"), 0o755); err != nil {
			t.Fatal(err)
		}
		if moved, _ := migrateLegacyClone(providerDir, "acme/team/app", "acme", src); moved {
			t.Fatal("must not move onto an existing path")
		}
		if _, err := os.Stat(filepath.Join(providerDir, "team", "app", ".git")); err != nil {
			t.Fatalf("legacy clone must be untouched: %v", err)
		}
	})

	t.Run("not a repository", func(t *testing.T) {
		providerDir := filepath.Join(t.TempDir(), "gl")
		group := filepath.Join(providerDir, "team", "app", "nested")
		if err := os.MkdirAll(group, 0o755); err != nil {
			t.Fatal(err)
		}
		if moved, _ := migrateLegacyClone(providerDir, "acme/team/app", "acme", src); moved {
			t.Fatal("a group directory must not be moved")
		}
		if _, err := os.Stat(group); err != nil {
			t.Fatalf("group directory must be untouched: %v", err)
		}
	})

	t.Run("name outside owner", func(t *testing.T) {
		providerDir := legacyClone(t, src, "gl", "team/app")
		if moved, _ := migrateLegacyClone(providerDir, "other/team/app", "acme", src); moved {
			t.Fatal("a name outside the owner has no legacy location")
		}
	})
}

func TestCloneIfNeededMigratesInsteadOfRecloning(t *testing.T) {
	src := newSourceRepo(t)
	providerDir := legacyClone(t, src, "gl", "team/app")
	if err := os.WriteFile(filepath.Join(providerDir, "team", "app", "local.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:   config.Config{},
		Provider: config.Provider{Name: "gl", Owner: "acme"},
	})

	head, err := cloneIfNeeded(ctx, "acme/team/app", src, providerDir+string(os.PathSeparator))
	if err != nil || head == "" {
		t.Fatalf("cloneIfNeeded: head=%q err=%v", head, err)
	}
	if data, _ := os.ReadFile(filepath.Join(providerDir, "acme", "team", "app", "local.txt")); string(data) != "keep" {
		t.Fatal("the existing clone should have been moved, not cloned again")
	}
}
