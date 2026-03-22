package watchers

import (
	"context"
	"fmt"
	"github.com/go-git/go-git/v5/plumbing/object"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/logging"
	"regexp"
	"strings"
)

func ProcessContributorWatchers(ctx context.Context, commit *object.Commit, repoState *config.RepoState) {
	if commit == nil || repoState == nil {
		return
	}

	if repoState.Contributors == nil {
		repoState.Contributors = map[string]config.ContributorState{}
	}

	newlyInsertedKeys := map[string]bool{}
	seenNewByRole := map[string]contributorObservation{}
	for _, obs := range collectContributorObservations(commit) {
		if obs.Key == "" {
			continue
		}

		isNew := upsertContributor(repoState, obs.Signature)
		if isNew {
			newlyInsertedKeys[obs.Key] = true
		}
		if !isNew && !newlyInsertedKeys[obs.Key] {
			continue
		}
		roleKey := obs.Key + "|" + obs.Role
		seenNewByRole[roleKey] = obs
	}
	if len(seenNewByRole) == 0 {
		return
	}

	ctxValue := ctx.Value("ctx").(config.ContextStruct)
	for name, w := range ctxValue.Config.Watchers {
		w.Name = name
		if !shouldEvaluateContributorWatcher(w, ctxValue.Provider.Name, repoState.Name) {
			continue
		}
		for _, obs := range seenNewByRole {
			if !watcherModeContainsSignature(w.AuthorOrCommitter, obs.Role) {
				continue
			}
			if shouldExcludeBotContributor(w, obs.Signature) {
				continue
			}

			events.Trigger(ctx, logging.Event{
				Name: w.Name,
				Type: logging.ContributorFirstSeen,
				Meta: logging.ContributorEventMeta{
					RepoName:      repoState.Name,
					CommitHash:    commit.Hash.String()[0:8],
					FirstLine:     strings.Split(commit.Message, "\n")[0],
					CommitterName: strings.TrimSpace(obs.Signature.Name),
					CommitterMail: strings.TrimSpace(obs.Signature.Email),
					Time:          obs.Signature.When,
					Provider:      ctxValue.Provider,
					RepoState:     *repoState,
				},
				Message: strings.TrimSpace(fmt.Sprintf(
					"First-time %s in %s: %s <%s> (%s)",
					obs.Role,
					repoState.Name,
					strings.TrimSpace(obs.Signature.Name),
					strings.TrimSpace(obs.Signature.Email),
					commit.Hash.String()[0:8],
				)),
			})
		}
	}
}

func shouldEvaluateContributorWatcher(w config.Watcher, providerName string, repoName string) bool {
	if w.Type != "contributor-first-seen" {
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

func canonicalContributorKey(signature object.Signature) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(signature.Email))
	if email != "" {
		return "email:" + email, true
	}

	name := strings.ToLower(strings.TrimSpace(signature.Name))
	if name != "" {
		return "name:" + name, true
	}

	return "", false
}

type contributorObservation struct {
	Role      string
	Key       string
	Signature object.Signature
}

func collectContributorObservations(commit *object.Commit) []contributorObservation {
	if commit == nil {
		return nil
	}
	observations := make([]contributorObservation, 0, 2)
	if key, ok := canonicalContributorKey(commit.Committer); ok {
		observations = append(observations, contributorObservation{
			Role:      "committer",
			Key:       key,
			Signature: commit.Committer,
		})
	}
	if key, ok := canonicalContributorKey(commit.Author); ok {
		observations = append(observations, contributorObservation{
			Role:      "author",
			Key:       key,
			Signature: commit.Author,
		})
	}
	return observations
}

func upsertContributor(repoState *config.RepoState, signature object.Signature) bool {
	if repoState == nil {
		return false
	}
	key, ok := canonicalContributorKey(signature)
	if !ok {
		return false
	}

	contributor, exists := repoState.Contributors[key]
	if exists {
		contributor.Commits += 1
		contributor.LastSeen = signature.When
		if contributor.Name == "" {
			contributor.Name = signature.Name
		}
		if contributor.Email == "" {
			contributor.Email = strings.TrimSpace(signature.Email)
		}
		repoState.Contributors[key] = contributor
		return false
	}

	repoState.Contributors[key] = config.ContributorState{
		Name:      strings.TrimSpace(signature.Name),
		Email:     strings.TrimSpace(signature.Email),
		FirstSeen: signature.When,
		LastSeen:  signature.When,
		Commits:   1,
	}
	return true
}

func watcherModeContainsSignature(mode string, role string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "committer":
		return role == "committer"
	case "author":
		return role == "author"
	case "both":
		return role == "author" || role == "committer"
	default:
		return role == "committer"
	}
}

func shouldExcludeBotContributor(w config.Watcher, signature object.Signature) bool {
	if !w.ExcludeBots {
		return false
	}

	patterns := w.BotPatterns
	if len(patterns) == 0 {
		patterns = []string{
			`(?i)\[bot\]`,
			`(?i)\bbot\b`,
			`(?i)dependabot`,
		}
	}

	target := strings.TrimSpace(signature.Name) + " <" + strings.TrimSpace(signature.Email) + ">"
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		r, err := regexp.Compile(p)
		if err != nil {
			panic(err)
		}
		if r.MatchString(target) {
			return true
		}
	}
	return false
}
