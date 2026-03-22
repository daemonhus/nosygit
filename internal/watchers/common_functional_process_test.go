package watchers

import (
	"context"
	"errors"
	"nosygit/internal/config"
	"reflect"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type fakePatch struct {
	filePatches []diff.FilePatch
}

func (p fakePatch) FilePatches() []diff.FilePatch { return p.filePatches }
func (p fakePatch) Message() string               { return "" }

type fakeFilePatch struct {
	from diff.File
	to   diff.File
}

func (p fakeFilePatch) IsBinary() bool                { return false }
func (p fakeFilePatch) Files() (diff.File, diff.File) { return p.from, p.to }
func (p fakeFilePatch) Chunks() []diff.Chunk          { return nil }

type fakeFile struct {
	path string
}

func (f fakeFile) Hash() plumbing.Hash     { return plumbing.ZeroHash }
func (f fakeFile) Mode() filemode.FileMode { return filemode.Regular }
func (f fakeFile) Path() string            { return f.path }

func buildChain(hashes ...string) CommitChain {
	var head *CommitChain
	var prev *CommitChain
	for _, h := range hashes {
		node := &CommitChain{
			Curr: &object.Commit{
				Hash:    plumbing.NewHash(h),
				Message: h,
			},
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

func restoreProcessWatcherSeams() {
	processContributorWatchersFn = ProcessContributorWatchers
	getPatchFromChainFn = getPatchFromChain
	evaluateWatchersFn = EvaluateWatchers
	evaluateCommitScopedWatchersFn = EvaluateCommitScopedWatchers
	triggerChainFn = func(chain CommitChain, ctx context.Context, results []WatcherResult) {
		chain.Trigger(ctx, results)
	}
}

func TestProcessWatchers_SyntheticDiffs_SequencingAndFiltering(t *testing.T) {
	restoreProcessWatcherSeams()
	defer restoreProcessWatcherSeams()

	oldHash := "1111111111111111111111111111111111111111"
	newHash := "2222222222222222222222222222222222222222"
	chain := buildChain(oldHash, newHash)
	repo := config.RepoState{Name: "acme/repo", Driver: "gh"}

	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config: config.Config{
			Watchers: map[string]config.Watcher{
				"watch-go-files": {
					Type: "file",
					Glob: "*.go",
					On:   []string{"CREATE"},
				},
			},
		},
		Provider:  config.Provider{Name: "gh"},
		RepoState: repo,
	})

	order := []string{}
	processContributorWatchersFn = func(ctx context.Context, commit *object.Commit, repoState *config.RepoState) {
		order = append(order, "contrib:"+commit.Hash.String())
	}
	getPatchFromChainFn = func(ctx context.Context, chain *CommitChain) (diff.Patch, error) {
		if chain.Curr.Hash.String() == oldHash {
			return fakePatch{
				filePatches: []diff.FilePatch{
					fakeFilePatch{from: nil, to: fakeFile{path: "cmd/app.go"}},
					fakeFilePatch{from: nil, to: fakeFile{path: "app.go"}},
				},
			}, nil
		}
		return fakePatch{
			filePatches: []diff.FilePatch{
				fakeFilePatch{from: nil, to: fakeFile{path: "readme.md"}},
			},
		}, nil
	}
	triggerChainFn = func(chain CommitChain, ctx context.Context, results []WatcherResult) {
		order = append(order, "trigger:"+chain.Curr.Hash.String()+":"+results[0].Path+":"+results[0].Operation)
	}

	chain.ProcessWatchers(ctx, &repo)

	want := []string{
		"contrib:" + oldHash,
		"trigger:" + oldHash + ":cmd/app.go:CREATE",
		"contrib:" + newHash,
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("unexpected process order\nwant=%#v\ngot=%#v", want, order)
	}
}

func TestProcessWatchers_PatchError_ContinuesToNextCommit(t *testing.T) {
	restoreProcessWatcherSeams()
	defer restoreProcessWatcherSeams()

	firstHash := "3333333333333333333333333333333333333333"
	secondHash := "4444444444444444444444444444444444444444"
	chain := buildChain(firstHash, secondHash)
	repo := config.RepoState{Name: "acme/repo", Driver: "gh"}

	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config: config.Config{
			Watchers: map[string]config.Watcher{
				"watch-go-files": {
					Type: "file",
					Glob: "*.go",
					On:   []string{"CREATE"},
				},
			},
		},
		Provider:  config.Provider{Name: "gh"},
		RepoState: repo,
	})

	order := []string{}
	processContributorWatchersFn = func(ctx context.Context, commit *object.Commit, repoState *config.RepoState) {
		order = append(order, "contrib:"+commit.Hash.String())
	}
	getPatchFromChainFn = func(ctx context.Context, chain *CommitChain) (diff.Patch, error) {
		if chain.Curr.Hash.String() == firstHash {
			return nil, errors.New("patch generation failed")
		}
		return fakePatch{
			filePatches: []diff.FilePatch{
				fakeFilePatch{from: nil, to: fakeFile{path: "service.go"}},
			},
		}, nil
	}
	triggerChainFn = func(chain CommitChain, ctx context.Context, results []WatcherResult) {
		order = append(order, "trigger:"+chain.Curr.Hash.String())
	}

	chain.ProcessWatchers(ctx, &repo)

	want := []string{
		"contrib:" + firstHash,
		"contrib:" + secondHash,
		"trigger:" + secondHash,
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("unexpected process order when first patch fails\nwant=%#v\ngot=%#v", want, order)
	}
}
