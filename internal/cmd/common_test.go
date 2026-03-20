package cmd

import (
	"nosygit/internal/config"
	"testing"
)

func TestIsInOrg(t *testing.T) {
	if !isInOrg("acme-org/service", "acme-org") {
		t.Fatal("expected repo to match org prefix")
	}
	if isInOrg("other-org/service", "acme-org") {
		t.Fatal("did not expect repo to match org prefix")
	}
	if !isInOrg("any/repo", "") {
		t.Fatal("empty org should match all repos")
	}
}

func TestShouldEvaluateRepoVisibilityWatcher(t *testing.T) {
	watcher := config.Watcher{
		Type:         "repo-visibility",
		Provider:     "github-acme",
		Organisation: "acme-org",
	}
	change := RepoVisibilityChange{
		Provider: config.Provider{
			Name: "github-acme",
		},
		RepoName:  "acme-org/example",
		Operation: "EDIT",
	}

	if !shouldEvaluateRepoVisibilityWatcher(watcher, change) {
		t.Fatal("expected watcher to evaluate for matching provider/org")
	}

	watcher.Type = "repo"
	if shouldEvaluateRepoVisibilityWatcher(watcher, change) {
		t.Fatal("did not expect non repo-visibility watcher to evaluate")
	}

	watcher.Type = "repo-visibility"
	watcher.On = []string{"ADD"}
	if shouldEvaluateRepoVisibilityWatcher(watcher, change) {
		t.Fatal("did not expect operation mismatch to evaluate")
	}

	watcher.On = nil
	watcher.VisibilityFrom = "private"
	watcher.VisibilityTo = "public"
	change.FromVisibility = "Private"
	change.ToVisibility = "Public"
	if !shouldEvaluateRepoVisibilityWatcher(watcher, change) {
		t.Fatal("expected watcher to evaluate for matching visibility transition")
	}

	watcher.VisibilityFrom = "public"
	if shouldEvaluateRepoVisibilityWatcher(watcher, change) {
		t.Fatal("did not expect visibility_from mismatch to evaluate")
	}

	watcher.VisibilityFrom = "private"
	watcher.VisibilityTo = "internal"
	if shouldEvaluateRepoVisibilityWatcher(watcher, change) {
		t.Fatal("did not expect visibility_to mismatch to evaluate")
	}
}

func TestShouldEvaluateBranchWatcher(t *testing.T) {
	watcher := config.Watcher{
		Type:         "branch",
		Provider:     "github-acme",
		Organisation: "acme-org",
		On:           []string{"ADD", "DELETE"},
	}
	change := BranchChange{
		Provider: config.Provider{
			Name: "github-acme",
		},
		RepoName:  "acme-org/example",
		Operation: "ADD",
		Branch:    "release",
	}

	if !shouldEvaluateBranchWatcher(watcher, change) {
		t.Fatal("expected branch watcher to evaluate for matching provider/org/op")
	}

	watcher.Type = "repo"
	if shouldEvaluateBranchWatcher(watcher, change) {
		t.Fatal("did not expect non-branch watcher to evaluate")
	}

	watcher.Type = "branch"
	watcher.On = []string{"DELETE"}
	if shouldEvaluateBranchWatcher(watcher, change) {
		t.Fatal("did not expect operation mismatch to evaluate")
	}
}
