package watchers

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"nosygit/internal/logging"
)

type lineMetrics struct {
	Added   int
	Changed int
	Deleted int
}

type blobMetrics struct {
	Added   int
	Changed int
	Deleted int
}

const defaultBlobMinSizeBytes = 10 * 1024

func EvaluateCommitScopedWatchers(ctx context.Context, chain *CommitChain, diffs []diff.FilePatch) []WatcherResult {
	if chain == nil || chain.Curr == nil {
		return nil
	}

	ctxValue := ctx.Value("ctx").(config.ContextStruct)
	results := make([]WatcherResult, 0)

	for _, w := range ctxValue.Config.Watchers {
		if !shouldEvaluateCommitScopedWatcher(w, ctxValue.Provider.Name, ctxValue.RepoState.Name) {
			continue
		}

		switch w.Type {
		case "commit-message":
			res, ok := EvaluateCommitMessageWatcher(w, chain.Curr)
			if ok {
				results = append(results, res...)
			}
		case "commit-metrics":
			res, ok := EvaluateCommitMetricsWatcher(w, diffs)
			if ok {
				results = append(results, res...)
			}
		case "blob":
			res, ok := EvaluateBlobWatcher(w, diffs)
			if ok {
				results = append(results, res...)
			}
		case "merge-stats":
			res, ok := EvaluateMergeStatsWatcher(ctx, w, chain)
			if ok {
				results = append(results, res...)
			}
		}
	}

	return results
}

func shouldEvaluateCommitScopedWatcher(w config.Watcher, providerName string, repoName string) bool {
	switch w.Type {
	case "commit-message", "commit-metrics", "merge-stats", "blob":
	default:
		return false
	}
	if w.Provider != "" && providerName != w.Provider {
		return false
	}
	if w.Organisation == "" {
		return true
	}
	return strings.HasPrefix(repoName, w.Organisation)
}

func EvaluateCommitMessageWatcher(w config.Watcher, commit *object.Commit) ([]WatcherResult, bool) {
	if commit == nil {
		return nil, false
	}
	if strings.TrimSpace(w.Regex) == "" {
		return nil, false
	}

	r, err := regexp.Compile(w.Regex)
	if err != nil {
		panic(err)
	}
	if !r.MatchString(commit.Message) {
		return nil, false
	}

	firstLine := strings.Split(commit.Message, "\n")[0]
	return []WatcherResult{{
		Name:    w.Name,
		Type:    logging.CommitMessage,
		Content: fmt.Sprintf("Commit message matched in %s: %s", commit.Hash.String()[0:8], firstLine),
	}}, true
}

func EvaluateCommitMetricsWatcher(w config.Watcher, diffs []diff.FilePatch) ([]WatcherResult, bool) {
	metrics := metricsForPatches(diffs)
	if metrics.Added == 0 && metrics.Changed == 0 && metrics.Deleted == 0 {
		return nil, false
	}
	if !metricsPassThresholds(metrics, w) {
		return nil, false
	}
	return []WatcherResult{{
		Name: w.Name,
		Type: logging.CommitMetrics,
		Content: fmt.Sprintf(
			"Commit metrics: added=%d changed=%d deleted=%d",
			metrics.Added, metrics.Changed, metrics.Deleted,
		),
	}}, true
}

func EvaluateBlobWatcher(w config.Watcher, diffs []diff.FilePatch) ([]WatcherResult, bool) {
	minSize := w.MinSize
	if minSize <= 0 {
		minSize = defaultBlobMinSizeBytes
	}
	metrics := blobMetricsForPatches(diffs, minSize)
	if metrics.Added == 0 && metrics.Changed == 0 && metrics.Deleted == 0 {
		return nil, false
	}
	return []WatcherResult{{
		Name: w.Name,
		Type: logging.Blob,
		Content: fmt.Sprintf(
			"Blob stats: added=%d changed=%d deleted=%d",
			metrics.Added, metrics.Changed, metrics.Deleted,
		),
	}}, true
}

func EvaluateMergeStatsWatcher(ctx context.Context, w config.Watcher, chain *CommitChain) ([]WatcherResult, bool) {
	if chain == nil || chain.Curr == nil || !isMergeCommit(chain.Curr) {
		return nil, false
	}

	commitsBetween := 0
	metrics := lineMetrics{}
	for cursor := chain.Prev; cursor != nil; cursor = cursor.Prev {
		if cursor.Curr == nil {
			continue
		}
		if isMergeCommit(cursor.Curr) {
			break
		}

		commitsBetween += 1
		patch, err := getPatchFromChainFn(ctx, cursor)
		if err != nil {
			continue
		}
		m := metricsForPatches(patch.FilePatches())
		metrics.Added += m.Added
		metrics.Changed += m.Changed
		metrics.Deleted += m.Deleted
	}
	if !metricsPassThresholds(metrics, w) {
		return nil, false
	}

	return []WatcherResult{{
		Name: w.Name,
		Type: logging.MergeStats,
		Content: fmt.Sprintf(
			"Merge stats: commits=%d added=%d changed=%d deleted=%d",
			commitsBetween, metrics.Added, metrics.Changed, metrics.Deleted,
		),
	}}, true
}

func isMergeCommit(commit *object.Commit) bool {
	if commit == nil {
		return false
	}
	return len(commit.ParentHashes) > 1
}

func metricsForPatches(diffs []diff.FilePatch) lineMetrics {
	total := lineMetrics{}
	for _, d := range diffs {
		m := metricsForChunks(d.Chunks())
		total.Added += m.Added
		total.Changed += m.Changed
		total.Deleted += m.Deleted
	}
	return total
}

func metricsForChunks(chunks []diff.Chunk) lineMetrics {
	metrics := lineMetrics{}
	addRun := 0
	delRun := 0
	flush := func() {
		if addRun == 0 && delRun == 0 {
			return
		}
		changed := minInt(addRun, delRun)
		metrics.Changed += changed
		metrics.Added += addRun - changed
		metrics.Deleted += delRun - changed
		addRun = 0
		delRun = 0
	}

	for _, c := range chunks {
		lines := countChunkLines(c.Content())
		switch c.Type() {
		case diff.Add:
			addRun += lines
		case diff.Delete:
			delRun += lines
		default:
			flush()
		}
	}
	flush()
	return metrics
}

func metricsPassThresholds(metrics lineMetrics, w config.Watcher) bool {
	if metrics.Added < w.MinAdded {
		return false
	}
	if metrics.Changed < w.MinChanged {
		return false
	}
	if metrics.Deleted < w.MinDeleted {
		return false
	}
	return true
}

func blobMetricsForPatches(diffs []diff.FilePatch, minSize int) blobMetrics {
	metrics := blobMetrics{}
	for _, d := range diffs {
		from, to := d.Files()
		_, op, ok := classifyFileOperation(from != nil, to != nil, pathOrEmpty(from), pathOrEmpty(to))
		if !ok {
			continue
		}
		if patchChangedBytes(d) < minSize {
			continue
		}

		switch op {
		case "CREATE":
			metrics.Added += 1
		case "EDIT":
			metrics.Changed += 1
		case "REMOVE":
			metrics.Deleted += 1
		}
	}
	return metrics
}

func patchChangedBytes(d diff.FilePatch) int {
	total := 0
	for _, c := range d.Chunks() {
		switch c.Type() {
		case diff.Add, diff.Delete:
			total += len(c.Content())
		}
	}
	return total
}

func countChunkLines(content string) int {
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
