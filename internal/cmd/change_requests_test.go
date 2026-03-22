package cmd

import (
	"nosygit/internal/storage"
	"testing"
)

func TestNormalizeChangeRequestState(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "open", want: "open"},
		{in: "opened", want: "open"},
		{in: "OPENED", want: "open"},
		{in: "closed", want: "closed"},
		{in: "merged", want: "merged"},
		{in: "draft", want: "draft"},
		{in: "  Open  ", want: "open"},
	}

	for _, tt := range tests {
		got := normalizeChangeRequestState(tt.in)
		if got != tt.want {
			t.Fatalf("normalizeChangeRequestState(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDeriveChangeRequestActions(t *testing.T) {
	t.Run("created open", func(t *testing.T) {
		actions := deriveChangeRequestActions(storage.ChangeRequestUpsertResult{
			Created: true,
			Current: storage.ChangeRequestRecord{State: "open"},
		})
		if len(actions) != 1 || actions[0] != changeRequestActionCreated {
			t.Fatalf("unexpected actions: %#v", actions)
		}
	})

	t.Run("closed and changed", func(t *testing.T) {
		prevComments := 1
		nextComments := 2
		actions := deriveChangeRequestActions(storage.ChangeRequestUpsertResult{
			Created: false,
			Previous: &storage.ChangeRequestRecord{
				State:         "open",
				Description:   "v1",
				SourceSHA:     "abc",
				CommentsCount: &prevComments,
			},
			Current: storage.ChangeRequestRecord{
				State:         "closed",
				Description:   "v2",
				SourceSHA:     "def",
				CommentsCount: &nextComments,
			},
		})

		required := map[string]bool{
			changeRequestActionClosed:     false,
			changeRequestActionNewCommits: false,
			changeRequestActionChanged:    false,
		}
		for _, action := range actions {
			if _, ok := required[action]; ok {
				required[action] = true
			}
		}
		for action, hit := range required {
			if !hit {
				t.Fatalf("expected action %q in %#v", action, actions)
			}
		}
	})
}
