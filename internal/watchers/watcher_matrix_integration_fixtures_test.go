package watchers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"nosygit/internal/logging"
)

func loadAllFixtureCommitsFromRepo(t *testing.T, r *git.Repository) []*object.Commit {
	t.Helper()
	iter, err := r.Log(&git.LogOptions{All: false})
	if err != nil {
		t.Fatalf("read fixture log: %v", err)
	}

	commits := make([]*object.Commit, 0, 8)
	for {
		c, err := iter.Next()
		if err != nil {
			break
		}
		commits = append(commits, c)
	}
	if len(commits) == 0 {
		t.Fatalf("fixture has zero commits")
	}
	return commits
}

func chainFromNewestFirst(commits []*object.Commit) CommitChain {
	var head *CommitChain
	var prev *CommitChain
	for i := len(commits) - 1; i >= 0; i-- {
		node := &CommitChain{
			Curr: commits[i],
			Prev: prev,
		}
		if prev != nil {
			prev.Next = node
		}
		if head == nil {
			head = node
		}
		prev = node
	}
	return *head
}

func firstChangedPathFromChain(t *testing.T, chain CommitChain) string {
	t.Helper()
	for cursor := &chain; cursor != nil; cursor = cursor.Next {
		if cursor.Prev == nil {
			continue
		}
		patch, err := getPatchFromChain(context.Background(), cursor)
		if err != nil {
			continue
		}
		for _, fp := range patch.FilePatches() {
			from, to := fp.Files()
			path, _, ok := classifyFileOperation(from != nil, to != nil, pathOrEmpty(from), pathOrEmpty(to))
			if ok && strings.TrimSpace(path) != "" {
				return path
			}
		}
	}
	t.Fatalf("unable to derive changed path from fixture commit chain")
	return ""
}

func hasMergeCommit(commits []*object.Commit) bool {
	for _, c := range commits {
		if len(c.ParentHashes) > 1 {
			return true
		}
	}
	return false
}

func runFixtureWatcherMatrix(t *testing.T, cfg config.Config, chain CommitChain, repoState config.RepoState) map[logging.EventType]int {
	t.Helper()
	restoreProcessWatcherSeams()
	defer restoreProcessWatcherSeams()

	processContributorWatchersFn = ProcessContributorWatchers
	getPatchFromChainFn = getPatchFromChain
	evaluateWatchersFn = EvaluateWatchers
	evaluateCommitScopedWatchersFn = EvaluateCommitScopedWatchers

	typeHits := map[logging.EventType]int{}
	triggerChainFn = func(chain CommitChain, ctx context.Context, results []WatcherResult) {
		for _, result := range results {
			typeHits[result.Type] += 1
		}
	}

	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:    cfg,
		Provider:  config.Provider{Name: "gh"},
		RepoState: repoState,
	})
	chain.ProcessWatchers(ctx, &repoState)
	return typeHits
}

func TestProcessWatchers_FixtureRepo_WatcherMatrix_Positive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fixture integration in short mode")
	}

	repo := openFixtureDotGitRepo(t, "merge-base")
	commits := loadAllFixtureCommitsFromRepo(t, repo)
	chain := chainFromNewestFirst(commits)
	filePath := firstChangedPathFromChain(t, chain)

	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"file-watch": {
				Type: "file",
				Glob: filePath,
				On:   []string{"CREATE", "EDIT", "REMOVE"},
			},
			"regex-watch": {
				Type:        "regex",
				Regex:       "(?s).+",
				PathExclude: "a^",
				On:          []string{"ADD", "UPDATE", "REMOVE"},
			},
			"commit-message-watch": {
				Type:  "commit-message",
				Regex: ".+",
			},
			"commit-metrics-watch": {
				Type:       "commit-metrics",
				MinAdded:   0,
				MinChanged: 0,
				MinDeleted: 0,
			},
			"blob-watch": {
				Type:    "blob",
				MinSize: 1,
			},
			"merge-stats-watch": {
				Type:       "merge-stats",
				MinAdded:   0,
				MinChanged: 0,
				MinDeleted: 0,
			},
		},
	}
	hits := runFixtureWatcherMatrix(t, cfg, chain, config.RepoState{Name: "acme/fixture", Driver: "gh"})

	required := []logging.EventType{
		logging.File,
		logging.Regex,
		logging.CommitMessage,
		logging.CommitMetrics,
		logging.Blob,
	}
	for _, typ := range required {
		if hits[typ] == 0 {
			t.Fatalf("expected watcher type %s to trigger at least once; hits=%v", typ.String(), hits)
		}
	}
	if hasMergeCommit(commits) && hits[logging.MergeStats] == 0 {
		t.Fatalf("expected merge-stats to trigger for merge-base fixture; hits=%v", hits)
	}
}

func TestProcessWatchers_FixtureRepo_WatcherMatrix_ThresholdFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fixture integration in short mode")
	}

	repo := openFixtureDotGitRepo(t, "merge-base")
	commits := loadAllFixtureCommitsFromRepo(t, repo)
	chain := chainFromNewestFirst(commits)
	filePath := firstChangedPathFromChain(t, chain)

	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"file-watch": {
				Type: "file",
				Glob: filePath,
				On:   []string{"CREATE", "EDIT", "REMOVE"},
			},
			"regex-watch": {
				Type:        "regex",
				Regex:       "(?s).+",
				PathExclude: "a^",
				On:          []string{"ADD", "UPDATE", "REMOVE"},
			},
			"commit-message-watch": {
				Type:  "commit-message",
				Regex: "^$",
			},
			"commit-metrics-watch": {
				Type:       "commit-metrics",
				MinAdded:   1_000_000,
				MinChanged: 0,
				MinDeleted: 0,
			},
			"blob-watch": {
				Type:    "blob",
				MinSize: 1_000_000,
			},
			"merge-stats-watch": {
				Type:       "merge-stats",
				MinAdded:   1_000_000,
				MinChanged: 0,
				MinDeleted: 0,
			},
		},
	}
	hits := runFixtureWatcherMatrix(t, cfg, chain, config.RepoState{Name: "acme/fixture", Driver: "gh"})

	if hits[logging.File] == 0 || hits[logging.Regex] == 0 {
		t.Fatalf("control watchers should still trigger; hits=%v", hits)
	}
	blocked := []logging.EventType{
		logging.CommitMessage,
		logging.CommitMetrics,
		logging.Blob,
		logging.MergeStats,
	}
	for _, typ := range blocked {
		if hits[typ] != 0 {
			t.Fatalf("expected watcher type %s to be filtered out by config; hits=%v", typ.String(), hits)
		}
	}
}

func runContributorFixtureWatcher(t *testing.T, w config.Watcher) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping fixture integration in short mode")
	}

	repo := openFixtureDotGitRepo(t, "merge-base")
	commits := loadAllFixtureCommitsFromRepo(t, repo)
	chain := chainFromNewestFirst(commits)
	repoState := config.RepoState{Name: "acme/fixture", Driver: "gh"}

	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"c": w,
		},
		Events: map[string]config.Event{
			"stdout-c": {
				Watcher: "c",
				Driver:  "stdout",
			},
		},
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:    cfg,
		Provider:  config.Provider{Name: "gh"},
		RepoState: repoState,
	})
	out := captureStdout(t, func() {
		chain.ProcessWatchers(ctx, &repoState)
	})
	return out
}

func TestProcessWatchers_FixtureRepo_ContributorOptions(t *testing.T) {
	tests := []struct {
		name       string
		watcher    config.Watcher
		wantInOut  string
		wantNoLine bool
	}{
		{
			name: "author mode",
			watcher: config.Watcher{
				Type:              "contributor-first-seen",
				AuthorOrCommitter: "author",
				ExcludeBots:       false,
				Organisation:      "acme/",
			},
			wantInOut: "First-time author",
		},
		{
			name: "committer mode",
			watcher: config.Watcher{
				Type:              "contributor-first-seen",
				AuthorOrCommitter: "committer",
				ExcludeBots:       false,
				Organisation:      "acme/",
			},
			wantInOut: "First-time committer",
		},
		{
			name: "exclude bots with custom pattern",
			watcher: config.Watcher{
				Type:              "contributor-first-seen",
				AuthorOrCommitter: "both",
				ExcludeBots:       true,
				BotPatterns:       []string{".*"},
				Organisation:      "acme/",
			},
			wantNoLine: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := runContributorFixtureWatcher(t, tt.watcher)
			if tt.wantNoLine {
				if strings.TrimSpace(out) != "" {
					t.Fatalf("expected no contributor output, got:\n%s", out)
				}
				return
			}

			records := parseJSONLines(t, out)
			if len(records) == 0 {
				t.Fatalf("expected contributor event output, got empty")
			}
			found := false
			for _, rec := range records {
				assertContractShape(t, rec)
				msg, _ := rec["message"].(string)
				if rec["kind"] == "watcher" && rec["event"] == "contributor-first-seen" && strings.Contains(msg, tt.wantInOut) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected watcher contributor event with message containing %q, got:\n%s", tt.wantInOut, out)
			}
		})
	}
}
