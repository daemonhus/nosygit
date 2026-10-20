package cmd

import (
	"path/filepath"
	"testing"

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

	if !isLegacyDuplicate(state, state.Repos[0]) {
		t.Fatal("prefixed entry with an unprefixed counterpart should be a duplicate")
	}
	if isLegacyDuplicate(state, state.Repos[1]) {
		t.Fatal("the unprefixed entry is the live one")
	}
	if isLegacyDuplicate(state, state.Repos[2]) {
		t.Fatal("a prefixed entry with no counterpart must still be processed")
	}
	if isLegacyDuplicate(state, state.Repos[3]) {
		t.Fatal("the same name under another provider is not a duplicate")
	}
}
