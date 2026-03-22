package watchers

import (
	"context"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"testing"
	"time"
)

func TestProcessContributorWatchersCanonicalByEmail(t *testing.T) {
	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"first-committer": {
				Name:         "first-committer",
				Type:         "contributor-first-seen",
				Provider:     "gh",
				Organisation: "acme/",
			},
		},
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:   cfg,
		Provider: config.Provider{Name: "gh"},
	})
	repoState := config.RepoState{Name: "acme/repo"}
	firstSeen := time.Now().Add(-time.Minute)

	ProcessContributorWatchers(ctx, &object.Commit{
		Hash:    plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Message: "feat: add endpoint",
		Committer: object.Signature{
			Name:  "Alice",
			Email: "Alice@Example.com",
			When:  firstSeen,
		},
	}, &repoState)
	ProcessContributorWatchers(ctx, &object.Commit{
		Hash:    plumbing.NewHash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		Message: "fix: adjust route",
		Committer: object.Signature{
			Name:  "Alice 2",
			Email: "alice@example.com",
			When:  time.Now(),
		},
	}, &repoState)

	if len(repoState.Contributors) != 1 {
		t.Fatalf("expected 1 contributor, got %d", len(repoState.Contributors))
	}
	contributor := repoState.Contributors["email:alice@example.com"]
	if contributor.Commits != 2 {
		t.Fatalf("expected commits=2, got %d", contributor.Commits)
	}
	if contributor.FirstSeen != firstSeen {
		t.Fatalf("expected first seen to remain unchanged")
	}
}

func TestProcessContributorWatchersFallsBackToName(t *testing.T) {
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config: config.Config{},
	})
	repoState := config.RepoState{Name: "acme/repo"}

	ProcessContributorWatchers(ctx, &object.Commit{
		Hash:    plumbing.NewHash("cccccccccccccccccccccccccccccccccccccccc"),
		Message: "docs: update",
		Committer: object.Signature{
			Name: "Release Bot",
			When: time.Now(),
		},
	}, &repoState)

	if len(repoState.Contributors) != 1 {
		t.Fatalf("expected 1 contributor, got %d", len(repoState.Contributors))
	}
	if _, ok := repoState.Contributors["name:release bot"]; !ok {
		t.Fatalf("expected contributor keyed by normalized name")
	}
}

func TestProcessContributorWatchersTracksAuthorAndCommitterState(t *testing.T) {
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config: config.Config{},
	})
	repoState := config.RepoState{Name: "acme/repo"}

	ProcessContributorWatchers(ctx, &object.Commit{
		Hash:    plumbing.NewHash("dddddddddddddddddddddddddddddddddddddddd"),
		Message: "feat: pair contribution",
		Author: object.Signature{
			Name:  "Author A",
			Email: "author@example.com",
			When:  time.Now().Add(-time.Minute),
		},
		Committer: object.Signature{
			Name:  "Committer C",
			Email: "committer@example.com",
			When:  time.Now(),
		},
	}, &repoState)

	if _, ok := repoState.Contributors["email:author@example.com"]; !ok {
		t.Fatalf("expected author to be tracked in contributor state")
	}
	if _, ok := repoState.Contributors["email:committer@example.com"]; !ok {
		t.Fatalf("expected committer to be tracked in contributor state")
	}
}

func TestWatcherModeContainsSignature(t *testing.T) {
	tests := []struct {
		mode string
		role string
		want bool
	}{
		{mode: "", role: "committer", want: true},
		{mode: "", role: "author", want: false},
		{mode: "author", role: "author", want: true},
		{mode: "author", role: "committer", want: false},
		{mode: "both", role: "author", want: true},
		{mode: "both", role: "committer", want: true},
	}

	for _, tc := range tests {
		if got := watcherModeContainsSignature(tc.mode, tc.role); got != tc.want {
			t.Fatalf("mode=%s role=%s: expected %v got %v", tc.mode, tc.role, tc.want, got)
		}
	}
}

func TestShouldExcludeBotContributor(t *testing.T) {
	signature := object.Signature{Name: "dependabot[bot]", Email: "bot@example.com"}

	if !shouldExcludeBotContributor(config.Watcher{ExcludeBots: true}, signature) {
		t.Fatalf("expected default bot patterns to match")
	}
	if shouldExcludeBotContributor(config.Watcher{ExcludeBots: false}, signature) {
		t.Fatalf("expected exclude_bots=false to skip filtering")
	}

	customBot := object.Signature{Name: "release-automation", Email: "ci@acme.com"}
	if !shouldExcludeBotContributor(config.Watcher{
		ExcludeBots: true,
		BotPatterns: []string{`(?i)release-automation`},
	}, customBot) {
		t.Fatalf("expected custom bot pattern to match")
	}
}
