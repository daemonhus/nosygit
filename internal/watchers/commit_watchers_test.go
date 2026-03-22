package watchers

import (
	"context"
	"reflect"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"nosygit/internal/logging"
)

type metricsFakePatch struct {
	filePatches []diff.FilePatch
}

func (p metricsFakePatch) FilePatches() []diff.FilePatch { return p.filePatches }
func (p metricsFakePatch) Message() string               { return "" }

type metricsFakeFilePatch struct {
	from   diff.File
	to     diff.File
	chunks []diff.Chunk
}

func (p metricsFakeFilePatch) IsBinary() bool                { return false }
func (p metricsFakeFilePatch) Files() (diff.File, diff.File) { return p.from, p.to }
func (p metricsFakeFilePatch) Chunks() []diff.Chunk          { return p.chunks }

type metricsFakeChunk struct {
	content string
	typ     diff.Operation
}

func (c metricsFakeChunk) Content() string      { return c.content }
func (c metricsFakeChunk) Type() diff.Operation { return c.typ }

type metricsFakeFile struct {
	path string
}

func (f metricsFakeFile) Hash() plumbing.Hash     { return plumbing.ZeroHash }
func (f metricsFakeFile) Mode() filemode.FileMode { return filemode.Regular }
func (f metricsFakeFile) Path() string            { return f.path }

func TestEvaluateCommitMessageWatcher(t *testing.T) {
	w := config.Watcher{Name: "cm", Type: "commit-message", Regex: "JIRA-[0-9]+"}
	commit := &object.Commit{
		Hash:    plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Message: "JIRA-123: add endpoint\n\nbody",
	}

	res, ok := EvaluateCommitMessageWatcher(w, commit)
	if !ok {
		t.Fatalf("expected watcher to match")
	}
	if len(res) != 1 {
		t.Fatalf("expected one result, got %d", len(res))
	}
	if res[0].Type != logging.CommitMessage {
		t.Fatalf("unexpected type: %v", res[0].Type)
	}
}

func TestMetricsForChunks_ClassifiesAddedChangedDeleted(t *testing.T) {
	m := metricsForChunks([]diff.Chunk{
		metricsFakeChunk{typ: diff.Delete, content: "old-1\nold-2\n"},
		metricsFakeChunk{typ: diff.Add, content: "new-1\n"},
		metricsFakeChunk{typ: diff.Equal, content: "ctx\n"},
		metricsFakeChunk{typ: diff.Add, content: "new-2\nnew-3\n"},
	})

	want := lineMetrics{Added: 2, Changed: 1, Deleted: 1}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("unexpected metrics\nwant=%#v\ngot=%#v", want, m)
	}
}

func TestEvaluateBlobWatcher_CountsCreateEditRemove(t *testing.T) {
	w := config.Watcher{Name: "blob-watch", Type: "blob", MinSize: 1}
	diffs := []diff.FilePatch{
		metricsFakeFilePatch{
			from: nil,
			to:   metricsFakeFile{path: "new.go"},
			chunks: []diff.Chunk{
				metricsFakeChunk{typ: diff.Add, content: "x\n"},
			},
		},
		metricsFakeFilePatch{
			from: metricsFakeFile{path: "old.go"},
			to:   metricsFakeFile{path: "old.go"},
			chunks: []diff.Chunk{
				metricsFakeChunk{typ: diff.Add, content: "x\n"},
			},
		},
		metricsFakeFilePatch{
			from: metricsFakeFile{path: "gone.go"},
			to:   nil,
			chunks: []diff.Chunk{
				metricsFakeChunk{typ: diff.Delete, content: "x\n"},
			},
		},
	}
	res, ok := EvaluateBlobWatcher(w, diffs)
	if !ok {
		t.Fatalf("expected blob watcher to match")
	}
	if len(res) != 1 {
		t.Fatalf("expected one result")
	}
	if res[0].Type != logging.Blob {
		t.Fatalf("unexpected type: %v", res[0].Type)
	}
}

func TestEvaluateBlobWatcher_DefaultMinSizeSkipsSmallPatches(t *testing.T) {
	w := config.Watcher{Name: "blob-watch", Type: "blob"}
	diffs := []diff.FilePatch{
		metricsFakeFilePatch{
			from: nil,
			to:   metricsFakeFile{path: "new.go"},
			chunks: []diff.Chunk{
				metricsFakeChunk{typ: diff.Add, content: "small\n"},
			},
		},
	}

	_, ok := EvaluateBlobWatcher(w, diffs)
	if ok {
		t.Fatalf("expected blob watcher to skip blobs under default min_size")
	}
}

func TestEvaluateCommitMetricsWatcher_Thresholds(t *testing.T) {
	w := config.Watcher{Name: "m", Type: "commit-metrics", MinAdded: 1, MinChanged: 2, MinDeleted: 0}
	diffs := []diff.FilePatch{
		metricsFakeFilePatch{
			from: metricsFakeFile{path: "a.go"},
			to:   metricsFakeFile{path: "a.go"},
			chunks: []diff.Chunk{
				metricsFakeChunk{typ: diff.Delete, content: "old-1\nold-2\n"},
				metricsFakeChunk{typ: diff.Add, content: "new-1\nnew-2\nnew-3\n"},
			},
		},
	}

	_, ok := EvaluateCommitMetricsWatcher(w, diffs)
	if !ok {
		t.Fatalf("expected thresholds to pass")
	}

	w.MinAdded = 2
	_, ok = EvaluateCommitMetricsWatcher(w, diffs)
	if ok {
		t.Fatalf("expected thresholds to fail when min_added is too high")
	}
}

func TestEvaluateMergeStatsWatcher_UsesCommitsSincePreviousMerge(t *testing.T) {
	restore := getPatchFromChainFn
	defer func() { getPatchFromChainFn = restore }()

	first := &CommitChain{
		Curr: &object.Commit{Hash: plumbing.NewHash("1111111111111111111111111111111111111111"), Message: "feat 1"},
	}
	second := &CommitChain{
		Curr: &object.Commit{Hash: plumbing.NewHash("2222222222222222222222222222222222222222"), Message: "feat 2"},
		Prev: first,
	}
	first.Next = second
	merge := &CommitChain{
		Curr: &object.Commit{
			Hash:         plumbing.NewHash("3333333333333333333333333333333333333333"),
			Message:      "merge branch",
			ParentHashes: []plumbing.Hash{plumbing.NewHash("aaaa"), plumbing.NewHash("bbbb")},
		},
		Prev: second,
	}
	second.Next = merge

	getPatchFromChainFn = func(ctx context.Context, chain *CommitChain) (diff.Patch, error) {
		switch chain.Curr.Hash.String() {
		case second.Curr.Hash.String():
			return metricsFakePatch{
				filePatches: []diff.FilePatch{
					metricsFakeFilePatch{
						from: metricsFakeFile{path: "a.go"},
						to:   metricsFakeFile{path: "a.go"},
						chunks: []diff.Chunk{
							metricsFakeChunk{typ: diff.Delete, content: "old\n"},
							metricsFakeChunk{typ: diff.Add, content: "new\n"},
						},
					},
				},
			}, nil
		case first.Curr.Hash.String():
			return metricsFakePatch{
				filePatches: []diff.FilePatch{
					metricsFakeFilePatch{
						from: nil,
						to:   metricsFakeFile{path: "b.go"},
						chunks: []diff.Chunk{
							metricsFakeChunk{typ: diff.Add, content: "x\n"},
						},
					},
				},
			}, nil
		default:
			return metricsFakePatch{}, nil
		}
	}

	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{})
	res, ok := EvaluateMergeStatsWatcher(ctx, config.Watcher{Name: "merge-metrics", Type: "merge-stats", MinAdded: 1, MinChanged: 1}, merge)
	if !ok {
		t.Fatalf("expected merge stats watcher to trigger")
	}
	if len(res) != 1 {
		t.Fatalf("expected one result")
	}
	if res[0].Type != logging.MergeStats {
		t.Fatalf("unexpected type: %v", res[0].Type)
	}

	_, ok = EvaluateMergeStatsWatcher(ctx, config.Watcher{Name: "merge-metrics", Type: "merge-stats", MinAdded: 99}, merge)
	if ok {
		t.Fatalf("expected merge stats threshold to filter out event")
	}
}
