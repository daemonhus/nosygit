package watchers

import (
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/gobwas/glob"
	"nosygit/internal/config"
	"nosygit/internal/logging"
)

func EvaluateFileWatcherForPatches(w config.Watcher, diffs []diff.FilePatch) ([]WatcherResult, bool) {
	var results []WatcherResult

	for _, d := range diffs {
		from, to := d.Files()
		path, op, ok := classifyFileOperation(from != nil, to != nil, pathOrEmpty(from), pathOrEmpty(to))
		if !ok {
			continue
		}

		// @todo add glob and regex compilation - no error checking this deep
		g := glob.MustCompile(w.Glob)
		if !g.Match(path) {
			continue
		}

		if !w.OperationContains(op) {
			continue
		}

		results = append(results, WatcherResult{
			Name:      w.Name,
			Type:      logging.File,
			Path:      path,
			Operation: op,
		})
	}

	if len(results) == 0 {
		return []WatcherResult{}, false
	}
	return results, true
}

func classifyFileOperation(hasFrom bool, hasTo bool, fromPath string, toPath string) (string, string, bool) {
	if !hasFrom && !hasTo {
		return "", "", false
	}
	if !hasFrom && hasTo {
		return toPath, "CREATE", true
	}
	if hasFrom && !hasTo {
		return fromPath, "REMOVE", true
	}
	return toPath, "EDIT", true
}

func pathOrEmpty(f diff.File) string {
	if f == nil {
		return ""
	}
	return f.Path()
}
