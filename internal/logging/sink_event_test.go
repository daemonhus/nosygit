package logging

import (
	"nosygit/internal/config"
	"strings"
	"testing"
	"time"
)

func TestBuildSinkEventV1_Watcher(t *testing.T) {
	ts := time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)
	event := Event{
		Name:    "watch-commit-message",
		Type:    CommitMessage,
		Message: "commit message matched",
		Meta: FileEventMeta{
			Path:       "src/app.go",
			Operation:  "EDIT",
			CommitHash: "abc12345",
			FirstLine:  "feat: add x",
			Author:     "alice",
			Time:       ts,
			Provider:   config.Provider{Name: "gh"},
			RepoState:  config.RepoState{Name: "acme/api"},
		},
	}

	out := BuildSinkEventV1(event, "stdout")
	if out.Version != "v1" {
		t.Fatalf("version mismatch: %q", out.Version)
	}
	if out.Kind != "watcher" {
		t.Fatalf("kind mismatch: %q", out.Kind)
	}
	if out.Event != "commit-message" {
		t.Fatalf("event mismatch: %q", out.Event)
	}
	if out.Context.Watcher != "watch-commit-message" {
		t.Fatalf("watcher mismatch: %q", out.Context.Watcher)
	}
	if out.Context.Provider != "gh" || out.Context.Repo != "acme/api" {
		t.Fatalf("unexpected context: %#v", out.Context)
	}
	if got := out.Payload["operation"]; got != "EDIT" {
		t.Fatalf("operation mismatch: %#v", got)
	}
}

func TestBuildSinkEventV1_RemoteBranchName(t *testing.T) {
	event := Event{
		Name:    "branch-watch",
		Type:    Branch,
		Message: "branch added",
		Meta: BranchEventMeta{
			RepoName:   "acme/api",
			BranchName: "feature/a",
			Operation:  "ADD",
			Provider:   config.Provider{Name: "gh"},
		},
	}

	out := BuildSinkEventV1(event, "file-append")
	if out.Kind != "remote" {
		t.Fatalf("kind mismatch: %q", out.Kind)
	}
	if out.Event != "branch-added" {
		t.Fatalf("event mismatch: %q", out.Event)
	}
	if out.Context.Watcher != "" {
		t.Fatalf("remote context should not include watcher: %#v", out.Context)
	}
	if out.Context.Sink != "file-append" {
		t.Fatalf("sink mismatch: %q", out.Context.Sink)
	}
}

func TestMarshalSinkEventV1_AppendsNewline(t *testing.T) {
	body, err := MarshalSinkEventV1(Event{Type: CommitNew, Message: "new commit"}, "stdout")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.HasSuffix(string(body), "\n") {
		t.Fatalf("expected trailing newline, got %q", string(body))
	}
}
