package cmd

import (
	"context"
	"nosygit/internal/config"
	"nosygit/internal/storage"
	"nosygit/internal/watchers"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
)

func PersistAndEmitNewCommits(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState) {
	ctxValue := ctx.Value("ctx").(config.ContextStruct)
	if ctxValue.Store == nil || chain == nil {
		return
	}

	for curr := chain; curr != nil; curr = curr.Next {
		if curr.Curr == nil {
			continue
		}
		commit := curr.Curr
		inserted, err := ctxValue.Store.InsertCommitIfNew(storage.RepoRef{
			Driver:   repoState.Driver,
			Name:     repoState.Name,
			CloneURL: repoState.CloneUrl,
		}, commitInput(commit, time.Now()))
		if err != nil || !inserted {
			continue
		}

		EvaluateCommitWatchers(
			ctx,
			repoState,
			commit.Hash.String()[0:8],
			firstLine(commit.Message),
			strings.TrimSpace(commit.Author.Name),
			strings.TrimSpace(commit.Author.Email),
			strings.TrimSpace(commit.Committer.Name),
			strings.TrimSpace(commit.Committer.Email),
			commit.Committer.When,
		)
	}
}

func commitInput(commit *object.Commit, seenAt time.Time) storage.CommitInput {
	return storage.CommitInput{
		SHA:            commit.Hash.String(),
		AuthorName:     strings.TrimSpace(commit.Author.Name),
		AuthorEmail:    strings.TrimSpace(commit.Author.Email),
		CommitterName:  strings.TrimSpace(commit.Committer.Name),
		CommitterEmail: strings.TrimSpace(commit.Committer.Email),
		CommittedAt:    commit.Committer.When,
		MessageTitle:   firstLine(commit.Message),
		Message:        commit.Message,
		FirstSeenAt:    seenAt,
		Source:         "poll",
	}
}

func firstLine(message string) string {
	parts := strings.Split(message, "\n")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
