package watchers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	fixtures "github.com/go-git/go-git-fixtures/v4"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"nosygit/internal/config"
)

type rootedFS interface {
	Root() string
}

func fixtureByTags(t *testing.T, tags ...string) *fixtures.Fixture {
	t.Helper()
	if len(tags) == 0 {
		t.Fatalf("fixture tags are required")
	}
	set := fixtures.All()
	for _, tag := range tags {
		set = set.ByTag(tag)
	}
	if len(set) == 0 {
		t.Fatalf("no fixture for tags %v", tags)
	}
	return set.One()
}

func fixtureWorktreeRootByTags(t *testing.T, tags ...string) string {
	t.Helper()
	f := fixtureByTags(t, tags...)
	fs := f.Worktree()
	rootFS, ok := fs.(rootedFS)
	if !ok {
		t.Fatalf("fixture worktree does not expose Root()")
	}
	t.Cleanup(func() {
		_ = fixtures.Clean()
	})
	return rootFS.Root()
}

func openFixtureDotGitRepo(t *testing.T, tags ...string) *git.Repository {
	t.Helper()
	f := fixtureByTags(t, tags...)
	dotgit := f.DotGit()
	storage := filesystem.NewStorageWithOptions(dotgit, cache.NewObjectLRUDefault(), filesystem.Options{KeepDescriptors: true})
	t.Cleanup(func() {
		storage.Close()
		_ = fixtures.Clean()
	})

	r, err := git.Open(storage, nil)
	if err != nil {
		t.Fatalf("open fixture dotgit repo: %v", err)
	}
	return r
}

func loadRecentFixtureCommitsFromRepo(t *testing.T, r *git.Repository) []*object.Commit {
	t.Helper()
	iter, err := r.Log(&git.LogOptions{All: false})
	if err != nil {
		t.Fatalf("read fixture log: %v", err)
	}

	commits := make([]*object.Commit, 0, 2)
	for len(commits) < 2 {
		c, err := iter.Next()
		if err != nil {
			break
		}
		commits = append(commits, c)
	}
	if len(commits) < 1 {
		t.Fatalf("fixture has fewer than 1 commit")
	}
	return commits
}

func loadRecentFixtureCommits(t *testing.T, path string) []*object.Commit {
	t.Helper()
	r, err := git.PlainOpen(path)
	if err != nil {
		t.Fatalf("open fixture repo: %v", err)
	}
	return loadRecentFixtureCommitsFromRepo(t, r)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return buf.String()
}

func parseJSONLines(t *testing.T, out string) []map[string]interface{} {
	t.Helper()
	lines := make([]map[string]interface{}, 0)
	s := bufio.NewScanner(strings.NewReader(out))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("expected JSON line output, got invalid line %q: %v", line, err)
		}
		lines = append(lines, record)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan output: %v", err)
	}
	return lines
}

func assertContractShape(t *testing.T, rec map[string]interface{}) {
	t.Helper()

	requiredString := []string{"version", "kind", "event", "ts", "message"}
	for _, k := range requiredString {
		v, ok := rec[k].(string)
		if !ok || strings.TrimSpace(v) == "" {
			t.Fatalf("missing/invalid %q in event: %#v", k, rec)
		}
	}
	if rec["version"] != "v1" {
		t.Fatalf("expected version v1, got %#v", rec["version"])
	}
	if _, ok := rec["context"].(map[string]interface{}); !ok {
		t.Fatalf("missing/invalid context object: %#v", rec)
	}
	if _, ok := rec["payload"].(map[string]interface{}); !ok {
		t.Fatalf("missing/invalid payload object: %#v", rec)
	}
}

func chainFromRecentCommits(commits []*object.Commit) CommitChain {
	if len(commits) == 1 {
		return CommitChain{Curr: commits[0]}
	}

	oldNode := &CommitChain{Curr: commits[1]}
	newNode := &CommitChain{
		Curr: commits[0],
		Prev: oldNode,
	}
	oldNode.Next = newNode
	return *oldNode
}

func assertFixtureProcessWatchersOutput(t *testing.T, chain CommitChain, repoState config.RepoState, expectOutput bool) {
	t.Helper()

	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"first-contributor": {
				Type:     "contributor-first-seen",
				Provider: "gh",
			},
			"match-any-diff": {
				Type:        "regex",
				Regex:       "(?s).+",
				PathExclude: "a^",
				On:          []string{"ADD", "UPDATE", "REMOVE"},
			},
		},
		Events: map[string]config.Event{
			"stdout-first-contributor": {
				Watcher: "first-contributor",
				Driver:  "stdout",
			},
			"stdout-any-diff": {
				Watcher: "match-any-diff",
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
	if expectOutput && strings.TrimSpace(out) == "" {
		t.Fatalf("expected watcher event output from fixture diff, got empty stdout")
	}
	if !expectOutput {
		return
	}

	records := parseJSONLines(t, out)
	if len(records) == 0 {
		t.Fatalf("expected at least one JSON event line, got none")
	}
	watcherEvents := 0
	for _, rec := range records {
		assertContractShape(t, rec)
		if rec["kind"] == "watcher" {
			watcherEvents++
		}
	}
	if watcherEvents == 0 {
		t.Fatalf("expected at least one watcher-kind event, got %#v", records)
	}
}

func TestProcessWatchers_FixtureRepo_EmitsEvent_ComplexScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fixture integration in short mode")
	}

	// Ensure integration test runs real watcher path, not overridden seams.
	processContributorWatchersFn = ProcessContributorWatchers
	getPatchFromChainFn = getPatchFromChain
	evaluateWatchersFn = EvaluateWatchers
	evaluateCommitScopedWatchersFn = EvaluateCommitScopedWatchers
	triggerChainFn = func(chain CommitChain, ctx context.Context, results []WatcherResult) {
		chain.Trigger(ctx, results)
	}

	tests := []struct {
		name       string
		worktree   bool
		tags       []string
		repoSuffix string
		expectOut  bool
	}{
		{name: "basic-worktree", worktree: true, tags: []string{"worktree"}, repoSuffix: "basic", expectOut: true},
		{name: "submodule-worktree", worktree: true, tags: []string{"worktree", "submodule"}, repoSuffix: "submodule", expectOut: true},
		{name: "no-master-head-worktree", worktree: true, tags: []string{"worktree", "main-branch", "no-master-head"}, repoSuffix: "main-branch", expectOut: false},
		{name: "merge-base-dotgit", worktree: false, tags: []string{"merge-base"}, repoSuffix: "merge-base", expectOut: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var commits []*object.Commit
			if tt.worktree {
				path := fixtureWorktreeRootByTags(t, tt.tags...)
				commits = loadRecentFixtureCommits(t, path)
			} else {
				repo := openFixtureDotGitRepo(t, tt.tags...)
				commits = loadRecentFixtureCommitsFromRepo(t, repo)
			}

			chain := chainFromRecentCommits(commits)

			repoState := config.RepoState{
				Name:   "acme/" + tt.repoSuffix,
				Driver: "gh",
			}
			assertFixtureProcessWatchersOutput(t, chain, repoState, tt.expectOut)
		})
	}
}
