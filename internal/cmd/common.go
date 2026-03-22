package cmd

import (
	"context"
	"fmt"
	"github.com/go-git/go-git/v5"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/logging"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func PrintBanner() {
	fmt.Fprint(os.Stderr, `
  .....      nosy     .--~  .....  '.
.":    "'-..  .    .' ..-'"    :". '
' '._ ' _.''"(     '-"''._ ' _.' '
     ~~~      '.          ~~~
              .'
            (
             ^---'

`)
}

func isInOrg(repoName string, org string) bool {
	if org == "" {
		return true
	}
	return strings.HasPrefix(repoName, org)
}

type RepoChange struct {
	Provider   config.Provider
	RepoState  config.RepoState
	RepoName   string
	Visibility string
	Operation  string
}

// EvaluateRepoWatchers triggers any relevant events given a Repo is seen or removed
func EvaluateRepoWatchers(ctx context.Context, change RepoChange) {
	cfg := ctx.Value("ctx").(config.ContextStruct).Config
	for _, w := range cfg.Watchers {
		if w.Type != "repo" || !w.OperationContains(change.Operation) {
			continue
		}
		if change.Provider.Name != w.Provider || !isInOrg(change.RepoName, w.Organisation) {
			continue
		}

		events.Trigger(ctx, logging.Event{
			Name: w.Name,
			Type: logging.Repo,
			Meta: logging.RepoEventMeta{
				Provider:   change.Provider,
				RepoState:  change.RepoState,
				RepoName:   change.RepoName,
				Visibility: change.Visibility,
				Operation:  change.Operation,
			},
			Message: strings.TrimSpace(fmt.Sprintf("Repo %s: %s (%s)", strings.ToLower(change.Operation), change.RepoName, change.Visibility)),
		})
	}
}

type RepoVisibilityChange struct {
	Provider       config.Provider
	RepoState      config.RepoState
	RepoName       string
	FromVisibility string
	ToVisibility   string
	Operation      string
}

type BranchChange struct {
	Provider  config.Provider
	RepoState config.RepoState
	RepoName  string
	Branch    string
	Operation string
}

// EvaluateRepoVisibilityWatchers triggers any relevant events given a Repo changes visibility
func EvaluateRepoVisibilityWatchers(ctx context.Context, change RepoVisibilityChange) {
	cfg := ctx.Value("ctx").(config.ContextStruct).Config
	for _, w := range cfg.Watchers {
		if !shouldEvaluateRepoVisibilityWatcher(w, change) {
			continue
		}

		events.Trigger(ctx, logging.Event{
			Name: w.Name,
			Type: logging.RepoVisibility,
			Meta: logging.RepoVisibilityEventMeta{
				Provider:  change.Provider,
				RepoState: change.RepoState,
				RepoName:  change.RepoName,
				From:      change.FromVisibility,
				To:        change.ToVisibility,
			},
			Message: strings.TrimSpace(fmt.Sprintf("%s changed visibility from %s to %s", change.RepoName, change.FromVisibility, change.ToVisibility)),
		})
	}
}

func EvaluateBranchWatchers(ctx context.Context, change BranchChange) {
	cfg := ctx.Value("ctx").(config.ContextStruct).Config
	for _, w := range cfg.Watchers {
		if !shouldEvaluateBranchWatcher(w, change) {
			continue
		}

		events.Trigger(ctx, logging.Event{
			Name: w.Name,
			Type: logging.Branch,
			Meta: logging.BranchEventMeta{
				RepoName:   change.RepoName,
				BranchName: change.Branch,
				Operation:  change.Operation,
				Provider:   change.Provider,
				RepoState:  change.RepoState,
			},
			Message: strings.TrimSpace(fmt.Sprintf("Branch %s in %s: %s", strings.ToLower(change.Operation), change.RepoName, change.Branch)),
		})
	}
}

func shouldEvaluateBranchWatcher(w config.Watcher, change BranchChange) bool {
	if w.Type != "branch" {
		return false
	}
	if len(w.On) > 0 && !w.OperationContains(change.Operation) {
		return false
	}
	if change.Provider.Name != w.Provider || !isInOrg(change.RepoName, w.Organisation) {
		return false
	}
	return true
}

func shouldEvaluateRepoVisibilityWatcher(w config.Watcher, change RepoVisibilityChange) bool {
	if w.Type != "repo-visibility" {
		return false
	}
	if len(w.On) > 0 && !w.OperationContains(change.Operation) {
		return false
	}
	if change.Provider.Name != w.Provider || !isInOrg(change.RepoName, w.Organisation) {
		return false
	}
	if !visibilityTransitionMatches(w.VisibilityFrom, w.VisibilityTo, change.FromVisibility, change.ToVisibility) {
		return false
	}
	return true
}

func visibilityTransitionMatches(expectedFrom string, expectedTo string, actualFrom string, actualTo string) bool {
	from := strings.ToLower(strings.TrimSpace(actualFrom))
	to := strings.ToLower(strings.TrimSpace(actualTo))
	wantFrom := strings.ToLower(strings.TrimSpace(expectedFrom))
	wantTo := strings.ToLower(strings.TrimSpace(expectedTo))

	if wantFrom != "" && wantFrom != from {
		return false
	}
	if wantTo != "" && wantTo != to {
		return false
	}
	return true
}

func EvaluateCommitWatchers(ctx context.Context, repoState config.RepoState, commitHash string, firstLine string, authorName string, authorEmail string, committerName string, committerEmail string, when time.Time) {
	cfg := ctx.Value("ctx").(config.ContextStruct).Config
	provider := ctx.Value("ctx").(config.ContextStruct).Provider
	for _, w := range cfg.Watchers {
		if w.Type != "commit-new" {
			continue
		}
		if w.Provider != "" && provider.Name != w.Provider {
			continue
		}
		if !isInOrg(repoState.Name, w.Organisation) {
			continue
		}

		events.Trigger(ctx, logging.Event{
			Name: w.Name,
			Type: logging.CommitNew,
			Meta: logging.CommitEventMeta{
				RepoName:      repoState.Name,
				CommitHash:    commitHash,
				FirstLine:     firstLine,
				AuthorName:    authorName,
				AuthorEmail:   authorEmail,
				CommitterName: committerName,
				CommitterMail: committerEmail,
				Time:          when,
				Provider:      provider,
				RepoState:     repoState,
			},
			Message: strings.TrimSpace(fmt.Sprintf("New commit in %s: %s %s", repoState.Name, commitHash, firstLine)),
		})
	}
}

func cloneIfNeeded(ctx context.Context, repoName string, cloneUrl string, clonePath string) (string, error) {
	rootPath := clonePath + repoName
	_, err := os.Stat(rootPath)
	if err == nil {
		repo, s, openErr := OpenRepoPath(rootPath)
		if openErr == nil {
			defer s.Close()
			headRef, headErr := repo.Head()
			if headErr != nil {
				return "", headErr
			}
			hash, ok := GetHashFromRef(headRef)
			if !ok {
				return "", nil
			}
			// Verify the HEAD commit is readable — catches missing/corrupt pack files.
			if _, commitErr := repo.CommitObject(headRef.Hash()); commitErr == nil {
				return hash, nil
			}
			// Fall through to corrupt repo handling.
		}
		// Repo directory exists but is corrupt — remove and re-clone.
		if rmErr := os.RemoveAll(rootPath); rmErr != nil {
			return "", rmErr
		}
	}

	if err == nil || os.IsNotExist(err) {
		ctxValue, _ := ctx.Value("ctx").(config.ContextStruct)
		shallow := normalizeShallowConfig(ctxValue.Config.Shallow)

		if mkErr := os.MkdirAll(clonePath, 0o755); mkErr != nil {
			return "", mkErr
		}

		cloneOpts := &git.CloneOptions{URL: cloneUrl}
		if shallow.Enabled {
			cloneOpts.Depth = shallow.Depth
		}
		if strings.HasPrefix(cloneUrl, "git@") {
			if home, err := os.UserHomeDir(); err == nil {
				cloneOpts.Auth = getSSHKeyAuth(filepath.Join(home, ".ssh", "id_ed25519"))
			}
		}
		_, err := git.PlainClone(rootPath, false, cloneOpts)
		if err != nil && err != git.ErrRepositoryAlreadyExists {
			return "", err
		}

		repo, s, err := OpenRepoPath(rootPath)
		if err != nil {
			return "", err
		}
		defer s.Close()

		headRef, err := repo.Head()
		if err != nil || headRef == nil {
			return "", err
		}
		hash, ok := GetHashFromRef(headRef)
		if !ok {
			return "", err
		}
		return hash, err
	}

	return "", nil
}
