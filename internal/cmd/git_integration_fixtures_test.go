package cmd

import (
	"context"
	"io"
	"reflect"
	"testing"

	fixtures "github.com/go-git/go-git-fixtures/v4"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"nosygit/internal/config"
	"nosygit/internal/watchers"
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

func fixtureWorktreePathByTags(t *testing.T, tags ...string) string {
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

func headAndLogCommitsFromPath(t *testing.T, path string) (string, []*object.Commit) {
	t.Helper()
	r, s, err := OpenRepoPath(path)
	if err != nil {
		t.Fatalf("open repo path: %v", err)
	}
	defer s.Close()

	headRef, err := r.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	head := headRef.Hash().String()

	iter, err := r.Log(&git.LogOptions{All: false})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	commits := CommitIterToSlice(iter)
	if len(commits) < 1 {
		t.Fatalf("fixture has insufficient commits: %d", len(commits))
	}
	return head, commits
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

type repoOnlyHandle struct {
	repo *git.Repository
}

func (r *repoOnlyHandle) Head() (*plumbing.Reference, error) {
	return r.repo.Head()
}

func (r *repoOnlyHandle) Log(opts *git.LogOptions) (object.CommitIter, error) {
	return r.repo.Log(opts)
}

func (r *repoOnlyHandle) Close() error { return nil }

func expectedFromOldHead(commits []*object.Commit, oldHead string) []string {
	idx := -1
	for i, c := range commits {
		if c.Hash.String() == oldHead {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}

	out := make([]string, 0, idx+1)
	for i := idx; i >= 0; i-- {
		out = append(out, commits[i].Hash.String())
	}
	return out
}

func runFixtureSelectionAssertions(t *testing.T, path string, repoState config.RepoState, deps runGitDeps, head string, want []string) {
	t.Helper()

	var events []string
	var persisted []string
	var watched []string
	deps.persistAndEmit = func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
		events = append(events, "persist")
		for curr := chain; curr != nil; curr = curr.Next {
			if curr.Curr != nil {
				persisted = append(persisted, curr.Curr.Hash.String())
			}
		}
	}
	deps.processWatchers = func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {
		events = append(events, "watch")
		for curr := &chain; curr != nil; curr = curr.Next {
			if curr.Curr != nil {
				watched = append(watched, curr.Curr.Hash.String())
			}
		}
	}

	updated, err := RunGitWithDeps(context.Background(), path, repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != head {
		t.Fatalf("expected updated head %s, got %s", head, updated.Head)
	}
	if !reflect.DeepEqual(events, []string{"persist", "watch"}) {
		t.Fatalf("unexpected stage sequence: %#v", events)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("unexpected persisted chain, want=%#v got=%#v", want, persisted)
	}
	if !reflect.DeepEqual(watched, want) {
		t.Fatalf("unexpected watcher chain, want=%#v got=%#v", want, watched)
	}
}

func TestRunGitWithDeps_FixtureRepo_SequencingAndSelection_WorktreeScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fixture integration in short mode")
	}

	tests := []struct {
		name string
		tags []string
	}{
		{name: "basic-worktree", tags: []string{"worktree"}},
		{name: "submodule-worktree", tags: []string{"worktree", "submodule"}},
		{name: "no-master-head-worktree", tags: []string{"worktree", "main-branch", "no-master-head"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := fixtureWorktreePathByTags(t, tt.tags...)
			head, commits := headAndLogCommitsFromPath(t, path)
			oldHead := "0000000000000000000000000000000000000000"
			want := []string{commits[0].Hash.String()}
			if len(commits) >= 2 {
				oldHead = commits[1].Hash.String()
				want = expectedFromOldHead(commits, oldHead)
			}

			deps := runGitDeps{
				openRepo: func(path string) (repoHandle, error) {
					r, s, err := OpenRepoPath(path)
					if err != nil {
						return nil, err
					}
					return &fsRepoHandle{repo: r, storage: s}, nil
				},
				runCommand: func(path string, args ...string) error { return nil },
				out:        io.Discard,
			}
			runFixtureSelectionAssertions(t, path, config.RepoState{Head: oldHead}, deps, head, want)
		})
	}
}

func TestRunGitWithDeps_FixtureRepo_SequencingAndSelection_MergeBaseDotGit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fixture integration in short mode")
	}

	repo := openFixtureDotGitRepo(t, "merge-base")
	headRef, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	head := headRef.Hash().String()
	iter, err := repo.Log(&git.LogOptions{All: false})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	commits := CommitIterToSlice(iter)
	if len(commits) < 1 {
		t.Fatalf("merge-base fixture has insufficient commits: %d", len(commits))
	}
	oldHead := "0000000000000000000000000000000000000000"
	want := []string{commits[0].Hash.String()}
	if len(commits) >= 2 {
		oldHead = commits[1].Hash.String()
		want = expectedFromOldHead(commits, oldHead)
	}

	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			return &repoOnlyHandle{repo: repo}, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		out:        io.Discard,
	}
	runFixtureSelectionAssertions(t, "/fixture/dotgit", config.RepoState{Head: oldHead}, deps, head, want)
}

func TestRunGitWithDeps_FixtureRepo_ReorderedIteratorStillCapturesNew(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fixture integration in short mode")
	}

	path := fixtureWorktreePathByTags(t, "worktree")
	head, commits := headAndLogCommitsFromPath(t, path)
	if len(commits) < 2 {
		t.Skip("fixture has fewer than 2 commits required for reorder test")
	}
	oldHead := commits[1].Hash.String()
	want := []string{oldHead, head}

	// Build an intentionally out-of-order iterator (old head before current head).
	reordered := make([]*object.Commit, 0, len(commits))
	reordered = append(reordered, commits[1], commits[0])
	reordered = append(reordered, commits[2:]...)

	openCalls := 0
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			openCalls++
			if openCalls == 2 {
				return &fakeRepoHandle{
					iter: &fakeCommitIter{commits: reordered},
				}, nil
			}

			r, s, err := OpenRepoPath(path)
			if err != nil {
				return nil, err
			}
			return &fsRepoHandle{repo: r, storage: s}, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			got := make([]string, 0, 2)
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					got = append(got, curr.Curr.Hash.String())
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected persisted chain from reordered iterator, want=%#v got=%#v", want, got)
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {},
		out:             io.Discard,
	}

	updated, err := RunGitWithDeps(context.Background(), path, config.RepoState{Head: oldHead}, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != head {
		t.Fatalf("expected updated head %s, got %s", head, updated.Head)
	}
}
