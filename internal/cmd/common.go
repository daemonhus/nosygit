package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"io"
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

// isCorruptRepoErr reports whether err clearly means the on-disk repository is
// damaged or not a repository, as opposed to a transient failure.
func isCorruptRepoErr(err error) bool {
	var packErr *packfile.Error
	return errors.Is(err, errNotGitRepo) ||
		errors.Is(err, git.ErrRepositoryNotExists) ||
		errors.Is(err, plumbing.ErrObjectNotFound) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &packErr)
}

// quarantineRepo moves a damaged clone into <clonePath>/.quarantine instead of
// deleting it, so nothing in it is lost.
func quarantineRepo(clonePath, repoName, rootPath string) (string, error) {
	dir := filepath.Join(clonePath, ".quarantine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := strings.ReplaceAll(repoName, string(os.PathSeparator), "__")
	dest := filepath.Join(dir, fmt.Sprintf("%s-%s", name, time.Now().UTC().Format("20060102T150405.000000000")))
	if err := os.Rename(rootPath, dest); err != nil {
		return "", fmt.Errorf("couldn't quarantine %s: %w", rootPath, err)
	}
	return dest, nil
}

func cloneIfNeeded(ctx context.Context, repoName string, cloneUrl string, clonePath string) (string, error) {
	rootPath := filepath.Join(clonePath, repoName)
	if !strings.HasPrefix(filepath.Clean(rootPath)+string(os.PathSeparator), filepath.Clean(clonePath)+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid repo name %q: resolves outside clone path", repoName)
	}
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
			_, commitErr := repo.CommitObject(headRef.Hash())
			if commitErr == nil {
				return hash, nil
			}
			openErr = commitErr
		}
		// Only clear signs of corruption justify replacing the clone; anything
		// else (fd exhaustion, locks, permissions) may be transient, so retry later.
		if !isCorruptRepoErr(openErr) {
			return "", fmt.Errorf("couldn't open existing clone %s: %w", rootPath, openErr)
		}
		quarantined, qErr := quarantineRepo(clonePath, repoName, rootPath)
		if qErr != nil {
			return "", qErr
		}
		events.EmitMetaWarning(ctx, "git", "REPO_QUARANTINED", "moved corrupt clone aside before re-cloning", map[string]interface{}{"repo": repoName, "from": rootPath, "to": quarantined, "error": openErr.Error()})
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
			auth, authErr := resolveSSHAuth()
			if authErr != nil {
				return "", fmt.Errorf("ssh auth for %s: %w", repoName, authErr)
			}
			cloneOpts.Auth = auth
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
