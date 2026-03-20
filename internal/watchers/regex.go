package watchers

import (
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"nosygit/internal/config"
	"nosygit/internal/logging"
	"regexp"
	"strings"
)

func EvaluateRegexWatcherForPatches(w config.Watcher, diffs []diff.FilePatch) ([]WatcherResult, bool) {
	var results []WatcherResult

	r, err := regexp.Compile(w.Regex)
	if err != nil {
		panic(err)
	}

	pex, err := regexp.Compile(w.PathExclude)
	if err != nil {
		panic(err)
	}

	for _, d := range diffs {
		from, to := d.Files()
		_, op, ok := classifyFileOperation(from != nil, to != nil, pathOrEmpty(from), pathOrEmpty(to))
		if !ok {
			continue
		}
		if op == "CREATE" {
			op = "ADD"
		}

		path := pathOrEmpty(to)
		if path == "" {
			path = pathOrEmpty(from)
		}

		for _, chunk := range d.Chunks() {
			// We don't care if the chunk regex moves around
			if chunk.Type() == 0 {
				continue
			}

			content := chunk.Content()

			// Cheaper of the two regexs
			pathMatch := pex.MatchString(path)
			if pathMatch {
				continue
			}

			match := r.MatchString(content)
			if !match {
				continue
			}

			if !w.OperationContains(op) {
				continue
			}

			results = append(results, WatcherResult{
				Name:      w.Name,
				Type:      logging.Regex,
				Path:      path,
				Operation: op,
				Content:   summarizeRegexContent(content),
			})
		}
	}

	if len(results) > 0 {
		return results, true
	}
	return results, false
}

func summarizeRegexContent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 240 {
			return line[:240] + "..."
		}
		return line
	}
	return ""
}
