package cmd

import (
	"context"
	"fmt"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/logging"
	"nosygit/internal/storage"
	"strings"
)

const (
	changeRequestActionCreated    = "created"
	changeRequestActionClosed     = "closed"
	changeRequestActionMerged     = "merged"
	changeRequestActionNewCommits = "new-commits"
	changeRequestActionChanged    = "changed"
)

func UpsertChangeRequest(ctx context.Context, in storage.ChangeRequestInput) (bool, error) {
	ctxValue := ctx.Value("ctx").(config.ContextStruct)
	if ctxValue.Store == nil {
		return false, fmt.Errorf("missing state store in context")
	}

	in.State = normalizeChangeRequestState(in.State)
	result, err := ctxValue.Store.UpsertChangeRequestWithResult(in)
	if err != nil {
		return false, err
	}

	actions := deriveChangeRequestActions(result)
	if len(actions) == 0 {
		return result.Created, nil
	}

	EvaluateChangeRequestWatchers(ctx, in, actions)
	return result.Created, nil
}

func EvaluateChangeRequestOpenedWatchers(ctx context.Context, in storage.ChangeRequestInput) {
	EvaluateChangeRequestWatchers(ctx, in, []string{changeRequestActionCreated})
}

func EvaluateChangeRequestWatchers(ctx context.Context, in storage.ChangeRequestInput, actions []string) {
	if len(actions) == 0 {
		return
	}

	actionSet := map[string]bool{}
	for _, a := range actions {
		actionSet[a] = true
	}

	ctxValue := ctx.Value("ctx").(config.ContextStruct)
	cfg := ctxValue.Config
	provider := ctxValue.Provider

	repoName := in.Repo.Name
	if repoName == "" {
		repoName = in.Repo.CloneURL
	}

	for _, w := range cfg.Watchers {
		if !isChangeRequestWatcher(w) {
			continue
		}
		if w.Provider != "" && provider.Name != w.Provider {
			continue
		}
		if !isInOrg(repoName, w.Organisation) {
			continue
		}
		selectedActions := watcherChangeRequestActions(w)
		if !matchesAnyAction(selectedActions, actionSet) {
			continue
		}

		action := firstMatchingAction(selectedActions, actionSet)

		events.Trigger(ctx, logging.Event{
			Name: w.Name,
			Type: logging.ChangeRequestOpened,
			Meta: logging.ChangeRequestEventMeta{
				RepoName:     repoName,
				Provider:     provider,
				RepoState:    ctxValue.RepoState,
				ExternalID:   in.ExternalID,
				Number:       in.Number,
				Kind:         in.Kind,
				Title:        in.Title,
				AuthorLogin:  in.AuthorLogin,
				State:        in.State,
				SourceBranch: in.SourceBranch,
				TargetBranch: in.TargetBranch,
				OpenedAt:     in.OpenedAt,
				UpdatedAt:    in.UpdatedAtProvider,
				Action:       action,
				Description:  in.Description,
				Comments:     in.CommentsCount,
				SourceSHA:    in.SourceSHA,
			},
			Message: fmt.Sprintf("%s %s in %s: %s", strings.ReplaceAll(in.Kind, "_", " "), action, repoName, in.Title),
		})
	}
}

func normalizeChangeRequestState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "open", "opened":
		return "open"
	case "closed":
		return "closed"
	case "merged":
		return "merged"
	default:
		return strings.ToLower(strings.TrimSpace(state))
	}
}

func deriveChangeRequestActions(result storage.ChangeRequestUpsertResult) []string {
	out := make([]string, 0, 4)
	if result.Created && result.Current.State == "open" {
		out = append(out, changeRequestActionCreated)
	}
	if result.Previous == nil {
		return out
	}

	if result.Previous.State != result.Current.State {
		if result.Current.State == "closed" {
			out = append(out, changeRequestActionClosed)
		}
		if result.Current.State == "merged" {
			out = append(out, changeRequestActionMerged)
		}
	}

	if strings.TrimSpace(result.Previous.SourceSHA) != "" &&
		strings.TrimSpace(result.Current.SourceSHA) != "" &&
		result.Previous.SourceSHA != result.Current.SourceSHA {
		out = append(out, changeRequestActionNewCommits)
	}

	if result.Previous.Description != result.Current.Description ||
		!sameOptionalInt(result.Previous.CommentsCount, result.Current.CommentsCount) {
		out = append(out, changeRequestActionChanged)
	}

	return dedupeActions(out)
}

func sameOptionalInt(a *int, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func dedupeActions(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, action := range in {
		if strings.TrimSpace(action) == "" || seen[action] {
			continue
		}
		seen[action] = true
		out = append(out, action)
	}
	return out
}

func isChangeRequestWatcher(w config.Watcher) bool {
	return w.Type == "change-request" || w.Type == "change-request-opened"
}

func watcherChangeRequestActions(w config.Watcher) []string {
	if w.Type == "change-request-opened" {
		return []string{changeRequestActionCreated}
	}
	if len(w.On) == 0 {
		return []string{
			changeRequestActionCreated,
			changeRequestActionClosed,
			changeRequestActionMerged,
			changeRequestActionNewCommits,
			changeRequestActionChanged,
		}
	}
	out := make([]string, 0, len(w.On))
	for _, action := range w.On {
		action = strings.ToLower(strings.TrimSpace(action))
		action = strings.ReplaceAll(action, "_", "-")
		out = append(out, action)
	}
	return out
}

func matchesAnyAction(selected []string, incoming map[string]bool) bool {
	for _, action := range selected {
		if incoming[action] {
			return true
		}
	}
	return false
}

func firstMatchingAction(selected []string, incoming map[string]bool) string {
	for _, action := range selected {
		if incoming[action] {
			return action
		}
	}
	for action := range incoming {
		return action
	}
	return ""
}
