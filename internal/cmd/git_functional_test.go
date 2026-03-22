package cmd

import (
	"context"
	"errors"
	"io"
	"nosygit/internal/config"
	"nosygit/internal/watchers"
	"reflect"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type fakeRepoHandle struct {
	head    *plumbing.Reference
	headErr error
	iter    object.CommitIter
	logErr  error
}

func (f *fakeRepoHandle) Head() (*plumbing.Reference, error) {
	return f.head, f.headErr
}

func (f *fakeRepoHandle) Log(*git.LogOptions) (object.CommitIter, error) {
	if f.logErr != nil {
		return nil, f.logErr
	}
	return f.iter, nil
}

func (f *fakeRepoHandle) Close() error { return nil }

type fakeCommitIter struct {
	commits []*object.Commit
	pos     int
}

func (f *fakeCommitIter) Next() (*object.Commit, error) {
	if f.pos >= len(f.commits) {
		return nil, io.EOF
	}
	c := f.commits[f.pos]
	f.pos++
	return c, nil
}

func (f *fakeCommitIter) ForEach(cb func(*object.Commit) error) error {
	for _, c := range f.commits {
		if err := cb(c); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeCommitIter) Close() {}

func testRef(hash string) *plumbing.Reference {
	return plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/main"), plumbing.NewHash(hash))
}

func testCommit(hash string) *object.Commit {
	return &object.Commit{Hash: plumbing.NewHash(hash), Message: hash}
}

func TestRunGitWithDeps_NoNewCommits_NoPersistOrWatchers(t *testing.T) {
	headHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repoState := config.RepoState{Head: headHash}

	openCalls := 0
	var commands [][]string
	persistCalls := 0
	watcherCalls := 0

	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			openCalls++
			return &fakeRepoHandle{head: testRef(headHash)}, nil
		},
		runCommand: func(path string, args ...string) error {
			commands = append(commands, args)
			return nil
		},
		listBranches: func(path string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		persistAndEmit: func(context.Context, *watchers.CommitChain, config.RepoState) {
			persistCalls++
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {
			watcherCalls++
		},
	}

	updated, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != headHash {
		t.Fatalf("expected head %s, got %s", headHash, updated.Head)
	}
	if openCalls != 1 {
		t.Fatalf("expected one openRepo call, got %d", openCalls)
	}
	if len(commands) != 2 || commands[0][0] != "reset" || commands[1][0] != "pull" {
		t.Fatalf("expected reset/pull command sequence, got %#v", commands)
	}
	if persistCalls != 0 {
		t.Fatalf("expected no persist call, got %d", persistCalls)
	}
	if watcherCalls != 0 {
		t.Fatalf("expected no watcher call, got %d", watcherCalls)
	}
}

func TestRunGitWithDeps_NewCommits_UsesMockedLogChain(t *testing.T) {
	oldHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	newHash := "cccccccccccccccccccccccccccccccccccccccc"
	repoState := config.RepoState{Head: oldHash}

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(newHash)},
		&fakeRepoHandle{
			iter: &fakeCommitIter{
				commits: []*object.Commit{
					testCommit(newHash),
					testCommit(oldHash),
				},
			},
		},
	}

	var persisted []string
	watcherCalls := 0
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			if len(repos) == 0 {
				return nil, errors.New("no fake repo configured")
			}
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					persisted = append(persisted, curr.Curr.Hash.String())
				}
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {
			watcherCalls++
		},
	}

	updated, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != newHash {
		t.Fatalf("expected updated head %s, got %s", newHash, updated.Head)
	}
	if len(persisted) != 2 || persisted[0] != oldHash || persisted[1] != newHash {
		t.Fatalf("unexpected persisted chain order: %#v", persisted)
	}
	if watcherCalls != 1 {
		t.Fatalf("expected watcher processing once, got %d", watcherCalls)
	}
}

func TestRunGitWithDeps_Sequencing_PersistBeforeWatchers(t *testing.T) {
	oldHash := "1111111111111111111111111111111111111111"
	newHash := "2222222222222222222222222222222222222222"
	repoState := config.RepoState{Head: oldHash}

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(newHash)},
		&fakeRepoHandle{
			iter: &fakeCommitIter{
				commits: []*object.Commit{
					testCommit(newHash),
					testCommit(oldHash),
				},
			},
		},
	}

	events := make([]string, 0, 2)
	var persisted []string
	var watched []string
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			events = append(events, "persist")
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					persisted = append(persisted, curr.Curr.Hash.String())
				}
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {
			events = append(events, "watchers")
			for curr := &chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					watched = append(watched, curr.Curr.Hash.String())
				}
			}
		},
	}

	_, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"persist", "watchers"}) {
		t.Fatalf("expected sequencing [persist watchers], got %#v", events)
	}
	if !reflect.DeepEqual(persisted, watched) {
		t.Fatalf("expected watcher chain to match persisted chain; persisted=%#v watched=%#v", persisted, watched)
	}
}

func TestRunGitWithDeps_OldHeadMissing_UsesOneCommitBack(t *testing.T) {
	missingOldHead := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	midHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	newHash := "cccccccccccccccccccccccccccccccccccccccc"
	repoState := config.RepoState{Head: missingOldHead}

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(newHash)},
		&fakeRepoHandle{
			iter: &fakeCommitIter{
				commits: []*object.Commit{
					testCommit(newHash),
					testCommit(midHash),
				},
			},
		},
	}

	var persisted []string
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					persisted = append(persisted, curr.Curr.Hash.String())
				}
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {},
	}

	updated, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != newHash {
		t.Fatalf("expected updated head %s, got %s", newHash, updated.Head)
	}
	if !reflect.DeepEqual(persisted, []string{midHash, newHash}) {
		t.Fatalf("expected fallback chain [mid,new], got %#v", persisted)
	}
}

func TestRunGitWithDeps_OldHeadMissingSingleCommit_NoPanic(t *testing.T) {
	missingOldHead := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newHash := "cccccccccccccccccccccccccccccccccccccccc"
	repoState := config.RepoState{Head: missingOldHead}

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(newHash)},
		&fakeRepoHandle{
			iter: &fakeCommitIter{
				commits: []*object.Commit{testCommit(newHash)},
			},
		},
	}

	var persisted []string
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					persisted = append(persisted, curr.Curr.Hash.String())
				}
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {},
	}

	updated, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != newHash {
		t.Fatalf("expected updated head %s, got %s", newHash, updated.Head)
	}
	if !reflect.DeepEqual(persisted, []string{newHash}) {
		t.Fatalf("expected single-commit persisted chain, got %#v", persisted)
	}
}

func TestRunGitWithDeps_LogError_ReturnsErrorAndUpdatesHead(t *testing.T) {
	oldHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	newHash := "cccccccccccccccccccccccccccccccccccccccc"
	repoState := config.RepoState{Head: oldHash}
	wantErr := errors.New("log failed")

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(newHash)},
		&fakeRepoHandle{logErr: wantErr},
	}

	persistCalls := 0
	watcherCalls := 0
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			persistCalls++
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {
			watcherCalls++
		},
	}

	updated, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if updated.Head != newHash {
		t.Fatalf("expected head to update to %s on log failure, got %s", newHash, updated.Head)
	}
	if persistCalls != 0 {
		t.Fatalf("expected no persist calls on log error, got %d", persistCalls)
	}
	if watcherCalls != 0 {
		t.Fatalf("expected no watcher calls on log error, got %d", watcherCalls)
	}
}

func TestRunGitWithDeps_CommandFailures_StopEarly(t *testing.T) {
	repoState := config.RepoState{Head: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}

	resetErr := errors.New("reset failed")
	pullErr := errors.New("pull failed")

	t.Run("reset error", func(t *testing.T) {
		runCalls := 0
		openCalls := 0
		deps := runGitDeps{
			openRepo: func(path string) (repoHandle, error) {
				openCalls++
				return &fakeRepoHandle{head: testRef("cccccccccccccccccccccccccccccccccccccccc")}, nil
			},
			runCommand: func(path string, args ...string) error {
				runCalls++
				return resetErr
			},
			listBranches: func(path string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		}

		_, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
		if !errors.Is(err, resetErr) {
			t.Fatalf("expected reset error, got %v", err)
		}
		if runCalls != 1 {
			t.Fatalf("expected one command call, got %d", runCalls)
		}
		if openCalls != 1 {
			t.Fatalf("expected one open call, got %d", openCalls)
		}
	})

	t.Run("pull error", func(t *testing.T) {
		runCalls := 0
		deps := runGitDeps{
			openRepo: func(path string) (repoHandle, error) {
				return &fakeRepoHandle{head: testRef("cccccccccccccccccccccccccccccccccccccccc")}, nil
			},
			runCommand: func(path string, args ...string) error {
				runCalls++
				if runCalls == 1 {
					return nil
				}
				return pullErr
			},
			listBranches: func(path string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		}

		_, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
		if !errors.Is(err, pullErr) {
			t.Fatalf("expected pull error, got %v", err)
		}
		if runCalls != 2 {
			t.Fatalf("expected two command calls, got %d", runCalls)
		}
	})
}

func TestRunGitWithDeps_MergeLikeHistory_PersistsAllNewCommitsInOrder(t *testing.T) {
	oldHash := "1010101010101010101010101010101010101010"
	featureHash := "2020202020202020202020202020202020202020"
	mergeHash := "3030303030303030303030303030303030303030"
	repoState := config.RepoState{Head: oldHash}

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(mergeHash)},
		&fakeRepoHandle{
			iter: &fakeCommitIter{
				commits: []*object.Commit{
					testCommit(mergeHash),
					testCommit(featureHash),
					testCommit(oldHash),
				},
			},
		},
	}

	var persisted []string
	var watched []string
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					persisted = append(persisted, curr.Curr.Hash.String())
				}
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {
			for curr := &chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					watched = append(watched, curr.Curr.Hash.String())
				}
			}
		},
	}

	updated, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != mergeHash {
		t.Fatalf("expected updated head %s, got %s", mergeHash, updated.Head)
	}
	wantOrder := []string{oldHash, featureHash, mergeHash}
	if !reflect.DeepEqual(persisted, wantOrder) {
		t.Fatalf("unexpected persisted order: want=%#v got=%#v", wantOrder, persisted)
	}
	if !reflect.DeepEqual(watched, wantOrder) {
		t.Fatalf("unexpected watcher order: want=%#v got=%#v", wantOrder, watched)
	}
}

func TestRunGitWithDeps_ReorderedLog_StillCapturesNewCommit(t *testing.T) {
	oldHash := "4040404040404040404040404040404040404040"
	newHash := "5050505050505050505050505050505050505050"
	repoState := config.RepoState{Head: oldHash}

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(newHash)},
		&fakeRepoHandle{
			iter: &fakeCommitIter{
				// Intentionally out-of-order (older commit before newer commit).
				commits: []*object.Commit{
					testCommit(oldHash),
					testCommit(newHash),
				},
			},
		},
	}

	var persisted []string
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					persisted = append(persisted, curr.Curr.Hash.String())
				}
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {},
	}

	updated, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != newHash {
		t.Fatalf("expected updated head %s, got %s", newHash, updated.Head)
	}
	if !reflect.DeepEqual(persisted, []string{oldHash, newHash}) {
		t.Fatalf("unexpected persisted order for reordered log: %#v", persisted)
	}
}

func TestRunGitWithDeps_BranchDiff_EmitsAddsAndDeletes(t *testing.T) {
	headHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repoState := config.RepoState{Name: "acme/repo", Head: headHash}

	branchCall := 0
	var seen []string
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			return &fakeRepoHandle{head: testRef(headHash)}, nil
		},
		runCommand: func(path string, args ...string) error { return nil },
		listBranches: func(path string) (map[string]struct{}, error) {
			branchCall++
			if branchCall == 1 {
				return map[string]struct{}{"main": {}, "feature": {}}, nil
			}
			return map[string]struct{}{"main": {}, "release": {}}, nil
		},
		emitBranch: func(ctx context.Context, repoState config.RepoState, operation string, branch string) {
			seen = append(seen, operation+":"+branch)
		},
	}

	_, err := RunGitWithDeps(context.Background(), "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if !reflect.DeepEqual(seen, []string{"ADD:release", "DELETE:feature"}) {
		t.Fatalf("unexpected branch changes: %#v", seen)
	}
}

func TestRunGitWithDeps_ShallowDeepen_MakesHeadReachable(t *testing.T) {
	oldHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	midHash := "cccccccccccccccccccccccccccccccccccccccc"
	newHash := "dddddddddddddddddddddddddddddddddddddddd"
	repoState := config.RepoState{Head: oldHash}

	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config: config.Config{
			Shallow: config.ShallowConfig{
				Enabled:   true,
				Depth:     10,
				MaxDeepen: 2,
			},
		},
	})

	repos := []repoHandle{
		&fakeRepoHandle{head: testRef(newHash)},
		&fakeRepoHandle{iter: &fakeCommitIter{commits: []*object.Commit{testCommit(newHash), testCommit(midHash)}}},
		&fakeRepoHandle{iter: &fakeCommitIter{commits: []*object.Commit{testCommit(newHash), testCommit(oldHash)}}},
	}

	var commands [][]string
	var persisted []string
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			r := repos[0]
			repos = repos[1:]
			return r, nil
		},
		runCommand: func(path string, args ...string) error {
			commands = append(commands, args)
			return nil
		},
		listBranches: func(path string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		persistAndEmit: func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
			for curr := chain; curr != nil; curr = curr.Next {
				if curr.Curr != nil {
					persisted = append(persisted, curr.Curr.Hash.String())
				}
			}
		},
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {},
	}

	updated, err := RunGitWithDeps(ctx, "/mock/repo", repoState, deps)
	if err != nil {
		t.Fatalf("RunGitWithDeps returned error: %v", err)
	}
	if updated.Head != newHash {
		t.Fatalf("expected updated head %s, got %s", newHash, updated.Head)
	}
	if !reflect.DeepEqual(persisted, []string{oldHash, newHash}) {
		t.Fatalf("unexpected persisted commits: %#v", persisted)
	}
	if len(commands) != 3 {
		t.Fatalf("expected reset/fetch/deepen commands, got %#v", commands)
	}
	if !reflect.DeepEqual(commands[1], []string{"fetch", "--prune", "--depth", "10", "origin"}) {
		t.Fatalf("unexpected shallow fetch args: %#v", commands[1])
	}
	if !reflect.DeepEqual(commands[2], []string{"fetch", "--deepen", "10", "origin"}) {
		t.Fatalf("unexpected deepen args: %#v", commands[2])
	}
}
