package watchers

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/logging"
	"strings"
)

type WatcherResult struct {
	Type      logging.EventType
	Name      string
	Path      string
	Content   string
	Operation string
}

type CommitChain struct {
	Prev *CommitChain
	Next *CommitChain
	Curr *object.Commit
}

var processContributorWatchersFn = ProcessContributorWatchers
var getPatchFromChainFn = getPatchFromChain
var evaluateWatchersFn = EvaluateWatchers
var evaluateCommitScopedWatchersFn = EvaluateCommitScopedWatchers
var triggerChainFn = func(chain CommitChain, ctx context.Context, results []WatcherResult) {
	chain.Trigger(ctx, results)
}

// ChainFromCommit reverses a commit chain, returning the tail cut at ref: `from`
func (chain CommitChain) ChainFromCommit(from string) CommitChain {
	currHash := chain.Curr.Hash.String()
	for currHash != from {
		if chain.Prev == nil {
			return chain
		}

		chain = *chain.Prev
		currHash = chain.Curr.Hash.String()
	}
	chain.Prev = nil
	return chain
}

func (chain CommitChain) ReferenceExistsInLog(hash string, max int) bool {
	if chain.Curr == nil {
		return false
	}
	if chain.Curr.Hash.String() == hash {
		return true
	}
	if chain.Prev == nil || max > 99 {
		return false
	}
	if chain.Curr.Hash != plumbing.ZeroHash {
		if chain.Curr.Hash.String() == hash {
			return true
		}
	}

	return (*chain.Prev).ReferenceExistsInLog(hash, max+1)
}

func (chain CommitChain) ProcessWatchers(ctx context.Context, repoState *config.RepoState) {
	currChain := &chain
	for currChain != nil {
		var results []WatcherResult
		processContributorWatchersFn(ctx, currChain.Curr, repoState)

		patch, err := getPatchFromChainFn(ctx, currChain)
		if err != nil {
			events.EmitMetaError(ctx, "watcher-engine", "DIFF_PATCH_FAILURE", "Failed to retrieve commit diff", map[string]interface{}{
				"commit": chain.Curr.Hash.String()[0:7],
				"error":  err.Error(),
			})
			currChain = currChain.Next
			continue
		}
		results, _ = evaluateWatchersFn(ctx, patch.FilePatches())
		results = append(results, evaluateCommitScopedWatchersFn(ctx, currChain, patch.FilePatches())...)

		if len(results) != 0 {
			triggerChainFn(*currChain, ctx, results)
		}

		currChain = currChain.Next
	}
}

func getPatchFromChain(ctx context.Context, chain *CommitChain) (patch diff.Patch, err error) {
	if chain.Prev != nil {
		// We have to defer recover here due to a bug in sergi/go-diff with an oob read
		defer func() {
			if r := recover(); r != nil {
				err = errors.New(r.(error).Error())
				events.EmitMetaWarning(ctx, "watcher-engine", "DIFF_PATCH_RECOVERED", "Recovered from diff processing panic", map[string]interface{}{
					"commit": chain.Curr.Hash.String()[0:7],
					"error":  err.Error(),
				})
			}
		}()

		patch, err := chain.Prev.Curr.Patch(chain.Curr)
		if err != nil {
			panic(err)
		}
		return patch, nil
	} else {
		// Trigger watchers for the initial commit of a repo (or as far back as we asked for)
		originTree := &object.Tree{}
		//firstTree, err := chain.Curr.Tree()
		//if err != nil {
		//	panic(err)
		//}
		//patch, err := originTree.Patch(firstTree)

		patch, err := originTree.Patch(originTree)
		if err != nil {
			panic(err)
		}

		return patch, nil
	}
}

func (chain CommitChain) Trigger(ctx context.Context, results []WatcherResult) {
	for _, r := range results {
		message := buildWatcherMessage(r)

		events.Trigger(ctx, logging.Event{
			Name: r.Name,
			Type: r.Type,
			Meta: logging.FileEventMeta{
				Path:       r.Path,
				Operation:  r.Operation,
				CommitHash: chain.Curr.Hash.String()[0:8],
				FirstLine:  strings.Split(chain.Curr.Message, "\n")[0],
				Author:     chain.Curr.Author.Name,
				Time:       chain.Curr.Author.When,
				Provider:   ctx.Value("ctx").(config.ContextStruct).Provider,
				RepoState:  ctx.Value("ctx").(config.ContextStruct).RepoState,
			},
			Message: message,
		})
	}
}

func buildWatcherMessage(r WatcherResult) string {
	if text := strings.TrimSpace(r.Content); text != "" {
		return text
	}
	switch r.Type {
	case logging.File:
		return fmt.Sprintf("File %s: %s", strings.ToLower(r.Operation), r.Path)
	case logging.Regex:
		return fmt.Sprintf("Regex match in %s (%s)", r.Path, strings.ToLower(r.Operation))
	case logging.CommitMessage:
		return "Commit message matched"
	case logging.CommitMetrics:
		return "Commit metrics threshold matched"
	case logging.MergeStats:
		return "Merge stats threshold matched"
	case logging.Blob:
		return "Blob size threshold matched"
	default:
		return "Watcher matched"
	}
}

func EvaluateWatchers(ctx context.Context, diffs []diff.FilePatch) ([]WatcherResult, bool) {
	var results []WatcherResult
	found := false
	cfg := ctx.Value("ctx").(config.ContextStruct).Config

	for k, w := range cfg.Watchers {
		w.Name = k
		var res []WatcherResult
		var f bool

		switch w.Type {
		case "regex":
			res, f = EvaluateRegexWatcherForPatches(w, diffs)
		case "file":
			res, f = EvaluateFileWatcherForPatches(w, diffs)
		}
		if f != true {
			continue
		}

		found = true
		for _, result := range res {
			results = append(results, result)
		}
	}

	return results, found
}
