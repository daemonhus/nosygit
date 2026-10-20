package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	gitconfigfmt "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
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
	rootCmd.AddCommand(processCmd)
	processCmd.PersistentFlags().StringVar(&configPath, "--config", "", "Relative path to config")
}

var processCmd = &cobra.Command{
	Use:   "process",
	Short: "Re-evaluate new commits against watchers for state-known repos.",
	Long:  "Fetches from origin and re-evaluates commits against watchers for state-known repos, without querying any provider API. Use --provider to target one provider or --all for all providers.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if !allProviders && providerName == "" {
			fmt.Fprintln(os.Stderr, "error: use --provider <name> or --all")
			_ = cmd.Usage()
			os.Exit(1)
		}
		if allProviders && providerName != "" {
			fmt.Fprintln(os.Stderr, "error: --all and --provider are mutually exclusive")
			_ = cmd.Usage()
			os.Exit(1)
		}

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

		for _, repoState := range state.Repos {
			if providerName != "" && repoState.Driver != providerName {
				continue
			}
			provider, ok := cfg.GetProvider(repoState.Driver)
			if !ok {
				events.EmitMetaWarning(context.Background(), "runner", "UNKNOWN_REPO_DRIVER", "repo in state has unknown provider", map[string]interface{}{"repo": repoState.Name, "driver": repoState.Driver})
				continue
			}
			if isLegacyDuplicate(state, repoState, provider.Owner) {
				emitVerboseMetaInfo(context.Background(), "runner", "LEGACY_REPO_SKIPPED", "skipping provider-prefixed duplicate of a repo", map[string]interface{}{"repo": repoState.Name, "driver": repoState.Driver})
				continue
			}
			clonePath := stateClonePath(cfg, provider, repoState)
			if _, migrateErr := migrateLegacyClone(clonePath, repoState.Name, provider.Owner, repoState.CloneUrl); migrateErr != nil {
				events.EmitMetaWarning(context.Background(), "runner", "LEGACY_CLONE_MIGRATION_FAILED", "failed moving a clone from the older layout", map[string]interface{}{"repo": repoState.Name, "error": migrateErr.Error()})
			}
			ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
				Config:    cfg,
				Provider:  provider,
				State:     state,
				RepoState: repoState,
				Store:     store,
				Verbose:   verbose,
			})
			updatedRepoState, err := RunGit(ctx, clonePath+repoState.Name, repoState)
			if err != nil {
				events.EmitMetaError(ctx, "git", "REPO_PROCESS_FAILED", "failed processing repository", map[string]interface{}{"repo": repoState.Name, "error": err.Error()})
				continue
			}
			state = state.SaveRepoState(updatedRepoState, true)
		}

		if err := state.WriteState(statePath); err != nil {
			log.Fatal(err)
		}
	},
}

func RunGit(ctx context.Context, path string, repoState config.RepoState) (config.RepoState, error) {
	return RunGitWithDeps(ctx, path, repoState, defaultRunGitDeps())
}

type repoHandle interface {
	Head() (*plumbing.Reference, error)
	Log(opts *git.LogOptions) (object.CommitIter, error)
	Close() error
}

// trackingHeadReader is implemented by handles that can report where origin's
// copy of the checked-out branch points. Commits are read from there, so
// detection does not depend on the local branch or the files on disk.
type trackingHeadReader interface {
	TrackingHead() (*plumbing.Reference, error)
}

// currentHeadRef returns the commit to analyse: origin's tip of the checked-out
// branch when available, otherwise HEAD.
func currentHeadRef(r repoHandle) (*plumbing.Reference, error) {
	if t, ok := r.(trackingHeadReader); ok {
		if ref, err := t.TrackingHead(); err == nil {
			return ref, nil
		}
	}
	return r.Head()
}

type runGitDeps struct {
	openRepo        func(path string) (repoHandle, error)
	runCommand      func(path string, args ...string) error
	runCommandOut   func(path string, args ...string) (string, error)
	listBranches    func(path string) (map[string]struct{}, error)
	fastForward     func(path string, lastHead string) (ffResult, error)
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

func (r *fsRepoHandle) TrackingHead() (*plumbing.Reference, error) {
	return trackingRef(r.repo)
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
		fastForward: func(path string, lastHead string) (ffResult, error) {
			repo, storage, err := OpenRepoPath(path)
			if err != nil {
				return ffResult{}, nil
			}
			defer storage.Close()
			return fastForwardIfClean(repo, lastHead)
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
	if d.fastForward == nil {
		d.fastForward = defaults.fastForward
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

	ff, ffErr := deps.fastForward(path, repoState.Head)
	if ffErr != nil {
		events.EmitMetaWarning(ctx, "git", "REPO_FAST_FORWARD_FAILED", "failed bringing the local branch up to origin", map[string]interface{}{"repo": repoState.Name, "error": ffErr.Error()})
	} else if ff.Reason != "" {
		events.EmitMetaInfo(ctx, "git", "REPO_WORKTREE_NOT_ADVANCED", "left the local branch and files as they are", map[string]interface{}{"repo": repoState.Name, "reason": ff.Reason})
	}

	headRef, err := currentHeadRef(r)
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
		// A zero From makes go-git start at HEAD; fakes ignore the options.
		gitLog, err := r.Log(&git.LogOptions{From: plumbing.NewHash(currentHead)})
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

func repoOriginAuth(repo *git.Repository) (transport.AuthMethod, error) {
	remote, err := repo.Remote("origin")
	if err != nil || remote.Config() == nil || len(remote.Config().URLs) == 0 {
		return nil, nil
	}
	if !strings.HasPrefix(remote.Config().URLs[0], "git@") {
		return nil, nil
	}
	return resolveSSHAuth()
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
	case "pull":
		auth, err := repoOriginAuth(repo)
		if err != nil {
			return err
		}
		return runPull(repo, auth)
	case "fetch":
		auth, err := repoOriginAuth(repo)
		if err != nil {
			return err
		}
		return runFetch(repo, auth, args[1:]...)
	default:
		return fmt.Errorf("unsupported command %q", strings.Join(args, " "))
	}
}

// runPull fetches origin. It never touches the worktree or branch; bringing the
// clone up to date is a separate, lossless step (see fastForwardIfClean).
func runPull(repo *git.Repository, auth transport.AuthMethod) error {
	return runFetch(repo, auth)
}

// trackingRef returns origin's reference for the branch HEAD points at.
func trackingRef(repo *git.Repository) (*plumbing.Reference, error) {
	headRef, err := repo.Reference(plumbing.HEAD, false)
	if err != nil {
		return nil, err
	}
	if headRef.Type() != plumbing.SymbolicReference {
		return nil, plumbing.ErrReferenceNotFound
	}
	return repo.Reference(plumbing.NewRemoteReferenceName("origin", headRef.Target().Short()), true)
}

// ffResult reports whether a clone was brought up to origin and, when it was
// left alone, why.
type ffResult struct {
	Advanced bool
	Reason   string
}

// fastForwardIfClean brings the checked-out branch and its files up to origin
// when that loses nothing. It leaves the clone exactly as it is when:
//   - tracked files are modified or staged;
//   - an untracked file would be overwritten by the incoming commit (other
//     untracked files, such as .DS_Store, do not block);
//   - the local branch has commits that are not on origin.
//
// lastHead is the commit nosygit last recorded for the repo. A local tip equal
// to it cannot carry local commits, which matters for shallow clones where the
// ancestry check cannot see that far back. Commits are read from origin either
// way, so nothing is missed when a clone is left behind.
func fastForwardIfClean(repo *git.Repository, lastHead string) (ffResult, error) {
	remote, err := trackingRef(repo)
	if err != nil {
		return ffResult{}, nil
	}
	local, err := repo.Head()
	if err != nil || local.Hash() == remote.Hash() {
		return ffResult{}, nil
	}
	wt, err := repo.Worktree()
	if err != nil {
		return ffResult{}, nil
	}
	// go-git ignores core.excludesFile, so load the global and system ignores.
	wt.Excludes = append(wt.Excludes, loadExcludePatterns()...)
	status, err := wt.Status()
	if err != nil {
		return ffResult{Reason: "could not read working tree status: " + err.Error()}, nil
	}

	var untracked []string
	for path, fileStatus := range status {
		switch {
		case fileStatus.Staging == git.Untracked && fileStatus.Worktree == git.Untracked:
			untracked = append(untracked, path)
		case fileStatus.Staging != git.Unmodified || fileStatus.Worktree != git.Unmodified:
			return ffResult{Reason: "tracked files have local changes"}, nil
		}
	}

	localCommit, err := repo.CommitObject(local.Hash())
	if err != nil {
		return ffResult{Reason: "local commit not readable"}, nil
	}
	remoteCommit, err := repo.CommitObject(remote.Hash())
	if err != nil {
		return ffResult{Reason: "origin commit not readable"}, nil
	}

	if lastHead == "" || local.Hash().String() != lastHead {
		ahead, ancestorErr := localCommit.IsAncestor(remoteCommit)
		if ancestorErr != nil || !ahead {
			if shallow, _ := repo.Storer.Shallow(); len(shallow) > 0 {
				return ffResult{Reason: "shallow history cannot confirm there are no local commits"}, nil
			}
			return ffResult{Reason: "local commits are not on origin"}, nil
		}
	}

	if len(untracked) > 0 {
		if conflict, found := untrackedConflict(localCommit, remoteCommit, untracked); found {
			return ffResult{Reason: "untracked file would be overwritten: " + conflict}, nil
		}
	}

	if err := fastForwardWorktree(wt, localCommit, remoteCommit); err != nil {
		return ffResult{}, err
	}
	return ffResult{Advanced: true}, nil
}

// fastForwardWorktree updates the files that differ between two commits, then
// moves the branch and index. It deliberately avoids go-git's hard reset, which
// also deletes untracked and ignored files. Only paths in the diff are touched.
func fastForwardWorktree(wt *git.Worktree, from, to *object.Commit) error {
	fromTree, err := from.Tree()
	if err != nil {
		return err
	}
	toTree, err := to.Tree()
	if err != nil {
		return err
	}
	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return err
	}

	for _, change := range changes {
		for _, entry := range []object.ChangeEntry{change.From, change.To} {
			if entry.Name != "" && hasGitComponent(entry.Name) {
				return fmt.Errorf("refusing path with a .git component: %s", entry.Name)
			}
		}
	}

	fs := wt.Filesystem
	for _, change := range changes {
		if change.To.Name == "" && change.From.Name != "" {
			if err := fs.Remove(change.From.Name); err != nil && !os.IsNotExist(err) {
				return err
			}
			pruneEmptyDirs(fs, filepath.Dir(change.From.Name))
		}
	}
	for _, change := range changes {
		name := change.To.Name
		if name == "" {
			continue
		}
		mode := change.To.TreeEntry.Mode
		if mode == filemode.Dir || mode == filemode.Submodule {
			continue
		}
		file, err := toTree.File(name)
		if err != nil {
			return err
		}
		if err := writeTreeFile(fs, name, file, mode); err != nil {
			return err
		}
	}

	return wt.Reset(&git.ResetOptions{Mode: git.MixedReset, Commit: to.Hash})
}

func hasGitComponent(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

func writeTreeFile(fs billy.Filesystem, name string, file *object.File, mode filemode.FileMode) error {
	if err := fs.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	if err := fs.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	reader, err := file.Reader()
	if err != nil {
		return err
	}
	defer reader.Close()

	if mode == filemode.Symlink {
		target, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		return fs.Symlink(string(target), name)
	}

	perm := os.FileMode(0o644)
	if mode == filemode.Executable {
		perm = 0o755
	}
	out, err := fs.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, reader); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// pruneEmptyDirs removes dir and its parents while they are empty.
func pruneEmptyDirs(fs billy.Filesystem, dir string) {
	for dir != "." && dir != "/" && dir != "" {
		entries, err := fs.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := fs.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// untrackedConflict returns an untracked path that the move from one commit to
// the other would write over, or whose parent or child path it would create.
func untrackedConflict(from, to *object.Commit, untracked []string) (string, bool) {
	fromTree, err := from.Tree()
	if err != nil {
		return untracked[0], true
	}
	toTree, err := to.Tree()
	if err != nil {
		return untracked[0], true
	}
	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return untracked[0], true
	}
	for _, change := range changes {
		incoming := change.To.Name
		if incoming == "" {
			continue
		}
		for _, path := range untracked {
			if path == incoming || strings.HasPrefix(incoming, path+"/") || strings.HasPrefix(path, incoming+"/") {
				return path, true
			}
		}
	}
	return "", false
}

// loadExcludePatterns reads the ignore patterns git applies outside a
// repository: core.excludesFile from ~/.gitconfig (or git's default
// ~/.config/git/ignore) and the system gitconfig's.
func loadExcludePatterns() []gitignore.Pattern {
	var patterns []gitignore.Pattern
	if home, err := os.UserHomeDir(); err == nil {
		file := configuredExcludesFile(filepath.Join(home, ".gitconfig"), home)
		if file == "" {
			base := os.Getenv("XDG_CONFIG_HOME")
			if base == "" {
				base = filepath.Join(home, ".config")
			}
			file = filepath.Join(base, "git", "ignore")
		}
		patterns = append(patterns, readIgnorePatterns(file)...)
	}
	if system, err := gitignore.LoadSystemPatterns(osfs.New("/")); err == nil {
		patterns = append(patterns, system...)
	}
	return patterns
}

// configuredExcludesFile returns core.excludesFile from a gitconfig, expanding
// a leading "~/", or "" when it is not set.
func configuredExcludesFile(gitconfig, home string) string {
	data, err := os.ReadFile(gitconfig)
	if err != nil {
		return ""
	}
	raw := gitconfigfmt.New()
	if err := gitconfigfmt.NewDecoder(bytes.NewReader(data)).Decode(raw); err != nil {
		return ""
	}
	file := raw.Section("core").Options.Get("excludesfile")
	if strings.HasPrefix(file, "~/") {
		file = filepath.Join(home, file[2:])
	}
	return file
}

func readIgnorePatterns(file string) []gitignore.Pattern {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var patterns []gitignore.Pattern
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, nil))
	}
	return patterns
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
	err := withAuthRetry(func() error { return repo.Fetch(opts) })
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

// errNotGitRepo means the path has neither a .git directory nor the layout of a
// bare repository, so there is nothing safe to open.
var errNotGitRepo = errors.New("path is not a git repository")

// OpenRepoPath opens the repository at path. A working tree needs a .git
// directory; a bare repository is opened without a worktree. It never treats
// the working tree as its own git dir, so a wrong path cannot be written into.
func OpenRepoPath(path string) (*git.Repository, *filesystem.Storage, error) {
	worktreeFs := osfs.New(path)
	var storageFs billy.Filesystem
	var wt billy.Filesystem

	gitDirInfo, err := worktreeFs.Stat(git.GitDirName)
	switch {
	case err == nil && gitDirInfo.IsDir():
		chrooted, err := worktreeFs.Chroot(git.GitDirName)
		if err != nil {
			return nil, nil, err
		}
		storageFs = chrooted
		wt = worktreeFs
	case err == nil:
		return nil, nil, fmt.Errorf("%w: %s/.git is not a directory", errNotGitRepo, path)
	case isBareRepoDir(worktreeFs):
		storageFs = worktreeFs
	default:
		return nil, nil, fmt.Errorf("%w: %s", errNotGitRepo, path)
	}

	s := filesystem.NewStorageWithOptions(storageFs, cache.NewObjectLRUDefault(), filesystem.Options{KeepDescriptors: true})
	r, err := git.Open(s, wt)
	if err != nil {
		return nil, s, err
	}
	return r, s, nil
}

func isBareRepoDir(fs billy.Filesystem) bool {
	if info, err := fs.Stat("objects"); err != nil || !info.IsDir() {
		return false
	}
	if info, err := fs.Stat("refs"); err != nil || !info.IsDir() {
		return false
	}
	_, err := fs.Stat("HEAD")
	return err == nil
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
