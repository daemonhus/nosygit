package cmd

import (
	"context"
	"fmt"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/spf13/cobra"
	"io"
	"log"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/watchers"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func init() {
	rootCmd.AddCommand(gitCmd)
	gitCmd.PersistentFlags().StringVar(&configPath, "--config", "", "Relative path to config")
}

var gitCmd = &cobra.Command{
	Use:   "git {path} [from-commit]",
	Short: "Process a local repository path and evaluate new commits against watchers.",
	Long:  "Process a local repository path and evaluate new commits against watchers.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.OpenConfig("./config.yml")
		if err != nil {
			log.Fatal(err)
		}
		state, err := config.OpenState(statePath)
		if err != nil {
			log.Fatal(err)
		}
		store, err := openStore(statePath)
		if err != nil {
			log.Fatal(err)
		}
		path := filepath.Clean(args[0])
		repoState, err := state.GetRepoStateByName(path)
		if err != nil {
			repoState, _ = state.GetRepoStateByName(filepath.Base(path))
		}
		repoState = ensureRepoStateDefaults(path, repoState)
		if len(args) == 2 {
			repoState.Head = args[1]
		}

		// Ensure the repo exists in persisted state before commit ingestion, otherwise
		// InsertCommitIfNew cannot resolve repo_id on first local runs.
		state = state.SaveRepoState(repoState, false)
		if err := state.WriteState(statePath); err != nil {
			log.Fatal(err)
		}

		provider, ok := cfg.GetProvider("local")
		if !ok {
			provider = config.Provider{Name: "local", Driver: "local"}
		}
		ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
			Config:    cfg,
			Provider:  provider,
			State:     state,
			RepoState: repoState,
			Store:     store,
			Verbose:   verbose,
		})
		events.EmitMetaInfo(ctx, "git", "LOCAL_GIT_RUN_START", "starting local git run", map[string]interface{}{"path": path, "repo": repoState.Name})

		updatedRepoState, err := RunGit(ctx, path, repoState)
		if err != nil {
			log.Fatal(err)
		}
		events.EmitMetaInfo(ctx, "git", "LOCAL_GIT_RUN_DONE", "finished local git run", map[string]interface{}{"path": path, "repo": updatedRepoState.Name, "head": updatedRepoState.Head})
		state = state.SaveRepoState(updatedRepoState, true)
		if err := state.WriteState(statePath); err != nil {
			log.Fatal(err)
		}
	},
}

func ensureRepoStateDefaults(path string, repoState config.RepoState) config.RepoState {
	cleanPath := filepath.Clean(path)
	if repoState.Name == "" {
		repoState.Name = filepath.Base(cleanPath)
	}
	if repoState.CloneUrl == "" {
		repoState.CloneUrl = detectOriginURL(cleanPath)
		if repoState.CloneUrl == "" {
			repoState.CloneUrl = cleanPath
		}
	}
	if repoState.Driver == "" {
		repoState.Driver = inferDriverFromCloneURL(repoState.CloneUrl)
	}
	return repoState
}

func detectOriginURL(path string) string {
	repo, storage, err := OpenRepoPath(path)
	if err != nil {
		return ""
	}
	defer storage.Close()

	remote, err := repo.Remote("origin")
	if err != nil {
		return ""
	}
	if remote.Config() == nil || len(remote.Config().URLs) == 0 {
		return ""
	}
	return strings.TrimSpace(remote.Config().URLs[0])
}

func inferDriverFromCloneURL(cloneURL string) string {
	lower := strings.ToLower(cloneURL)
	switch {
	case strings.Contains(lower, "github.com"):
		return "github"
	case strings.Contains(lower, "gitlab"):
		return "gitlab"
	default:
		return "local"
	}
}

func RunGit(ctx context.Context, path string, repoState config.RepoState) (config.RepoState, error) {
	return RunGitWithDeps(ctx, path, repoState, defaultRunGitDeps())
}

type repoHandle interface {
	Head() (*plumbing.Reference, error)
	Log(opts *git.LogOptions) (object.CommitIter, error)
	Close() error
}

type runGitDeps struct {
	openRepo        func(path string) (repoHandle, error)
	runCommand      func(path string, args ...string) error
	runCommandOut   func(path string, args ...string) (string, error)
	listBranches    func(path string) (map[string]struct{}, error)
	persistAndEmit  func(ctx context.Context, chain *watchers.CommitChain, repoState config.RepoState)
	processWatchers func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState)
	emitBranch      func(ctx context.Context, repoState config.RepoState, operation string, branch string)
	out             io.Writer
}

type fsRepoHandle struct {
	repo    *git.Repository
	storage *filesystem.Storage
}

func (r *fsRepoHandle) Head() (*plumbing.Reference, error) {
	return r.repo.Head()
}

func (r *fsRepoHandle) Log(opts *git.LogOptions) (object.CommitIter, error) {
	return r.repo.Log(opts)
}

func (r *fsRepoHandle) Close() error {
	if r.storage != nil {
		r.storage.Close()
	}
	return nil
}

func defaultRunGitDeps() runGitDeps {
	deps := runGitDeps{
		openRepo: func(path string) (repoHandle, error) {
			repo, storage, err := OpenRepoPath(path)
			if err != nil {
				return nil, err
			}
			return &fsRepoHandle{repo: repo, storage: storage}, nil
		},
		runCommand: func(path string, args ...string) error {
			return runRepoCommand(path, args...)
		},
		runCommandOut: func(path string, args ...string) (string, error) {
			return "", fmt.Errorf("command output mode unsupported for %q", strings.Join(args, " "))
		},
		persistAndEmit: PersistAndEmitNewCommits,
		processWatchers: func(chain watchers.CommitChain, ctx context.Context, repoState *config.RepoState) {
			chain.ProcessWatchers(ctx, repoState)
		},
		emitBranch: func(ctx context.Context, repoState config.RepoState, operation string, branch string) {
			ctxValue := ctx.Value("ctx").(config.ContextStruct)
			EvaluateBranchWatchers(ctx, BranchChange{
				Provider:  ctxValue.Provider,
				RepoState: repoState,
				RepoName:  repoState.Name,
				Branch:    branch,
				Operation: operation,
			})
		},
		out: os.Stdout,
	}
	deps.listBranches = func(path string) (map[string]struct{}, error) {
		return listRemoteOriginBranches(path)
	}
	return deps
}

func (d runGitDeps) withDefaults() runGitDeps {
	defaults := defaultRunGitDeps()
	if d.openRepo == nil {
		d.openRepo = defaults.openRepo
	}
	if d.runCommand == nil {
		d.runCommand = defaults.runCommand
	}
	if d.runCommandOut == nil {
		d.runCommandOut = func(path string, args ...string) (string, error) {
			err := d.runCommand(path, args...)
			return "", err
		}
	}
	if d.listBranches == nil {
		d.listBranches = func(path string) (map[string]struct{}, error) {
			return listRemoteOriginBranches(path)
		}
	}
	if d.persistAndEmit == nil {
		d.persistAndEmit = defaults.persistAndEmit
	}
	if d.processWatchers == nil {
		d.processWatchers = defaults.processWatchers
	}
	if d.emitBranch == nil {
		d.emitBranch = defaults.emitBranch
	}
	if d.out == nil {
		d.out = defaults.out
	}
	return d
}

func RunGitWithDeps(ctx context.Context, path string, repoState config.RepoState, deps runGitDeps) (config.RepoState, error) {
	deps = deps.withDefaults()
	ctxValue, _ := ctx.Value("ctx").(config.ContextStruct)
	shallow := normalizeShallowConfig(ctxValue.Config.Shallow)
	head := repoState.Head
	emitVerboseMetaInfo(ctx, "git", "REPO_SYNC_START", "starting repository sync", map[string]interface{}{"repo": repoState.Name, "path": path})
	r, err := deps.openRepo(path)
	if err != nil {
		if repoState.Name == "" {
			repoState.Name = filepath.Base(filepath.Clean(path))
		}
		if repoState.CloneUrl == "" {
			repoState.CloneUrl = path
		}
		return repoState, err
	}
	defer r.Close()

	branchesBefore, err := deps.listBranches(path)
	if err != nil {
		branchesBefore = map[string]struct{}{}
	}

	emitVerboseMetaInfo(ctx, "git", "REPO_RESET_START", "resetting repository worktree", map[string]interface{}{"repo": repoState.Name})
	err = deps.runCommand(path, "reset", "--hard")
	if err != nil {
		return repoState, err
	}

	emitVerboseMetaInfo(ctx, "git", "REPO_FETCH_START", "fetching repository updates", map[string]interface{}{"repo": repoState.Name, "shallow": shallow.Enabled})
	err = deps.runCommand(path, fetchArgsForConfig(shallow)...)
	if err != nil {
		return repoState, err
	}
	emitVerboseMetaInfo(ctx, "git", "REPO_FETCH_DONE", "finished fetching repository updates", map[string]interface{}{"repo": repoState.Name})
	branchesAfter, err := deps.listBranches(path)
	if err != nil {
		branchesAfter = map[string]struct{}{}
	}
	emitBranchDiff(ctx, deps, repoState, branchesBefore, branchesAfter)

	headRef, err := r.Head()
	if err != nil {
		return repoState, err
	}

	hash, ok := GetHashFromRef(headRef)
	if !ok {
		return repoState, nil
	}

	if hash == head {
		repoState.Head = hash
		emitVerboseMetaInfo(ctx, "git", "REPO_NO_NEW_COMMITS", "no new commits detected", map[string]interface{}{"repo": repoState.Name, "head": shortHash(hash)})
		return repoState, nil
	}

	// Catch cases where a repo didn't run successfully before, but has a commit
	if head == "" {
		head = hash
	}

	r, err = deps.openRepo(path)
	if err != nil {
		return repoState, err
	}
	defer r.Close()

	events.EmitMetaInfo(ctx, "git", "NEW_COMMITS_FOUND", "found new commits, analysing", map[string]interface{}{"repo": repoState.Name})

	commits, err := collectCommitsWithOptionalDeepen(path, r, hash, head, shallow, deps)
	if err != nil {
		repoState.Head = hash
		return repoState, err
	}
	fullChain := CommitSliceToChain(commits)

	if !fullChain.ReferenceExistsInLog(head, 0) {
		if shallow.Enabled {
			// In shallow mode, missing historical head after deepening indicates a history gap.
			// We skip backfill and move baseline to current head to avoid replaying stale history.
			events.EmitMetaWarning(ctx, "git", "SHALLOW_HISTORY_GAP", "previous head not reachable; skipping backfill", map[string]interface{}{"repo_path": path, "previous_head": shortHash(head)})
			repoState.Head = hash
			return repoState, nil
		}

		oldHead := head
		if fullChain.Prev != nil && fullChain.Prev.Curr != nil {
			head = fullChain.Prev.Curr.Hash.String()
			events.EmitMetaWarning(ctx, "git", "HEAD_NOT_FOUND", "could not find previous head in log, using fallback", map[string]interface{}{"repo_path": path, "fallback_head": shortHash(head), "previous_head": shortHash(oldHead)})
		}
	}
	chain := fullChain.ChainFromCommit(head)
	deps.persistAndEmit(ctx, &chain, repoState)

	deps.processWatchers(chain, ctx, &repoState)

	repoState.Head = hash
	emitVerboseMetaInfo(ctx, "git", "REPO_SYNC_DONE", "finished repository sync", map[string]interface{}{"repo": repoState.Name, "head": shortHash(hash)})
	return repoState, nil
}

func normalizeShallowConfig(in config.ShallowConfig) config.ShallowConfig {
	out := in
	if out.Depth <= 0 {
		out.Depth = 50
	}
	if out.MaxDeepen < 0 {
		out.MaxDeepen = 0
	}
	if out.MaxDeepen == 0 {
		out.MaxDeepen = 3
	}
	return out
}

func fetchArgsForConfig(cfg config.ShallowConfig) []string {
	if cfg.Enabled {
		return []string{"fetch", "--prune", "--depth", strconv.Itoa(cfg.Depth), "origin"}
	}
	return []string{"pull"}
}

func deepenArgs(depth int) []string {
	return []string{"fetch", "--deepen", strconv.Itoa(depth), "origin"}
}

func collectCommitsWithOptionalDeepen(path string, repo repoHandle, currentHead string, previousHead string, shallow config.ShallowConfig, deps runGitDeps) ([]*object.Commit, error) {
	collect := func(r repoHandle) ([]*object.Commit, error) {
		gitLog, err := r.Log(&git.LogOptions{All: false})
		if err != nil {
			return nil, err
		}
		commits := CommitIterToSlice(gitLog)
		return normalizeCommitsForHead(commits, currentHead), nil
	}

	commits, err := collect(repo)
	if err != nil {
		return nil, err
	}
	if previousHead == "" || previousHead == currentHead || !shallow.Enabled {
		return commits, nil
	}
	if CommitSliceToChain(commits).ReferenceExistsInLog(previousHead, 0) {
		return commits, nil
	}

	for i := 0; i < shallow.MaxDeepen; i++ {
		if err := deps.runCommand(path, deepenArgs(shallow.Depth)...); err != nil {
			return commits, nil
		}
		refreshed, openErr := deps.openRepo(path)
		if openErr != nil {
			return commits, nil
		}
		commits, err = collect(refreshed)
		refreshed.Close()
		if err != nil {
			return nil, err
		}
		if CommitSliceToChain(commits).ReferenceExistsInLog(previousHead, 0) {
			return commits, nil
		}
	}

	return commits, nil
}

func listRemoteOriginBranches(path string) (map[string]struct{}, error) {
	repo, storage, err := OpenRepoPath(path)
	if err != nil {
		return nil, err
	}
	defer storage.Close()

	branches := map[string]struct{}{}
	refs, err := repo.References()
	if err != nil {
		return nil, err
	}
	defer refs.Close()

	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref == nil {
			return nil
		}
		name := ref.Name().String()
		const prefix = "refs/remotes/origin/"
		if !strings.HasPrefix(name, prefix) {
			return nil
		}
		branch := strings.TrimPrefix(name, prefix)
		if branch == "" || branch == "HEAD" {
			return nil
		}
		branches[branch] = struct{}{}
		return nil
	})

	return branches, nil
}

func repoOriginAuth(repo *git.Repository) transport.AuthMethod {
	remote, err := repo.Remote("origin")
	if err != nil || remote.Config() == nil || len(remote.Config().URLs) == 0 {
		return nil
	}
	url := remote.Config().URLs[0]
	if !strings.HasPrefix(url, "git@") {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return getSSHKeyAuth(filepath.Join(home, ".ssh", "id_ed25519"))
}

func runRepoCommand(path string, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("empty command")
	}

	repo, storage, err := OpenRepoPath(path)
	if err != nil {
		return err
	}
	defer storage.Close()

	switch args[0] {
	case "reset":
		return runResetHard(repo)
	case "pull":
		return runPull(repo, repoOriginAuth(repo))
	case "fetch":
		return runFetch(repo, repoOriginAuth(repo), args[1:]...)
	default:
		return fmt.Errorf("unsupported command %q", strings.Join(args, " "))
	}
}

func runResetHard(repo *git.Repository) error {
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	headRef, err := repo.Head()
	if err != nil {
		return err
	}
	return wt.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: headRef.Hash()})
}

func runPull(repo *git.Repository, auth transport.AuthMethod) error {
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	err = wt.Pull(&git.PullOptions{RemoteName: "origin", Auth: auth})
	if err == nil || err == git.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}

func runFetch(repo *git.Repository, auth transport.AuthMethod, args ...string) error {
	opts := &git.FetchOptions{RemoteName: "origin", Auth: auth}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--prune":
			opts.Prune = true
		case "--depth":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --depth")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil {
				return err
			}
			opts.Depth = v
			i++
		case "--deepen":
			// go-git has no deepen option; fetch full history as the closest safe behavior.
			i++
		case "origin":
			opts.RemoteName = "origin"
		default:
			// Ignore unsupported flags to keep command shim tolerant.
		}
	}
	err := repo.Fetch(opts)
	if err == nil || err == git.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}

func emitBranchDiff(ctx context.Context, deps runGitDeps, repoState config.RepoState, before map[string]struct{}, after map[string]struct{}) {
	added := make([]string, 0)
	removed := make([]string, 0)

	for b := range after {
		if _, ok := before[b]; !ok {
			added = append(added, b)
		}
	}
	for b := range before {
		if _, ok := after[b]; !ok {
			removed = append(removed, b)
		}
	}

	sort.Strings(added)
	sort.Strings(removed)
	for _, b := range added {
		deps.emitBranch(ctx, repoState, "ADD", b)
	}
	for _, b := range removed {
		deps.emitBranch(ctx, repoState, "DELETE", b)
	}
}

func shortHash(hash string) string {
	if len(hash) < 7 {
		return hash
	}
	return hash[:7]
}

func OpenRepoPath(path string) (*git.Repository, *filesystem.Storage, error) {
	worktreeFs := osfs.New(path)
	storageFs := worktreeFs
	if _, err := worktreeFs.Stat(git.GitDirName); err == nil {
		chrooted, err := worktreeFs.Chroot(git.GitDirName)
		if err != nil {
			return nil, nil, err
		}
		storageFs = chrooted
	}

	s := filesystem.NewStorageWithOptions(storageFs, cache.NewObjectLRUDefault(), filesystem.Options{KeepDescriptors: true})
	r, err := git.Open(s, worktreeFs)
	if err != nil {
		return nil, s, err
	}
	return r, s, nil
}

// CommitIterToChain Work back from HEAD until we find the commit with no previous (init)
func CommitIterToChain(iter object.CommitIter) watchers.CommitChain {
	return CommitSliceToChain(CommitIterToSlice(iter))
}

func CommitIterToSlice(iter object.CommitIter) []*object.Commit {
	var commits []*object.Commit
	for {
		currCommit, err := iter.Next()
		if err != nil || currCommit == nil {
			return commits
		}
		commits = append(commits, currCommit)
	}
}

func CommitSliceToChain(commits []*object.Commit) watchers.CommitChain {
	chain := watchers.CommitChain{}
	if len(commits) == 0 {
		return chain
	}

	currChain := &chain
	for i, currCommit := range commits {
		currChain.Curr = currCommit
		if i == len(commits)-1 {
			break
		}

		newChain := watchers.CommitChain{
			Next: currChain,
		}
		currChain.Prev = &newChain
		currChain = &newChain
	}

	return chain
}

func normalizeCommitsForHead(commits []*object.Commit, headHash string) []*object.Commit {
	if len(commits) < 2 || headHash == "" {
		return commits
	}

	headIdx := -1
	for i, commit := range commits {
		if commit != nil && commit.Hash.String() == headHash {
			headIdx = i
			break
		}
	}
	if headIdx <= 0 {
		return commits
	}

	reordered := make([]*object.Commit, 0, len(commits))
	reordered = append(reordered, commits[headIdx])
	reordered = append(reordered, commits[:headIdx]...)
	reordered = append(reordered, commits[headIdx+1:]...)
	return reordered
}

func GetHashFromRef(reference *plumbing.Reference) (string, bool) {
	if reference == nil {
		return "", false
	}
	return reference.Hash().String(), true
}
