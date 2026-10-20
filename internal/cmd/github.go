package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-git/go-git/v5/plumbing/transport"
	GitSSH "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/go-github/v37/github"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"io"
	"net"
	"net/http"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/session"
	"nosygit/internal/storage"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func RunGithub(ctx context.Context) (int, config.State, []error) {
	state := ctx.Value("ctx").(config.ContextStruct).State
	provider := ctx.Value("ctx").(config.ContextStruct).Provider

	emitVerboseMetaInfo(ctx, "provider-sync", "GITHUB_SESSION_INIT", "initializing github session", map[string]interface{}{"provider": provider.Name})
	var gh *session.GithubSession
	gh, err := session.InitGithubSession()
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_SESSION_INIT_FAILED", "failed to initialize github session", map[string]interface{}{"provider": provider.Name, "error": err.Error()})
		return 0, state, []error{err}
	}

	var errs []error
	repos, err := getRepos(gh, provider.Owner)
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_FETCH_FAILED", "failed to fetch github repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "error": err.Error()})
		return 0, state, []error{err}
	}
	emitVerboseMetaInfo(ctx, "provider-sync", "PROVIDER_FETCHED", "fetched github repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "count": len(repos)})

	var wg sync.WaitGroup
	results := make(chan config.RepoState, len(repos))
	errorsChan := make(chan error, len(repos))

	for _, repo := range repos {
		wg.Add(1)
		go func(repo *github.Repository) {
			defer wg.Done()
			repoState, err := handleRepo(ctx, gh, repo)
			if err != nil {
				errorsChan <- err
				return
			}
			results <- repoState
		}(repo)
	}

	go func() {
		wg.Wait()
		close(results)
		close(errorsChan)
	}()

	repoStates, repoErrs := collectRepoStateAndErrors(results, errorsChan)
	errs = append(errs, repoErrs...)
	for _, result := range repoStates {
		state = state.SaveRepoState(result, false)
	}

	return len(repos), state, errs
}

func RunGithubCloneOnly(ctx context.Context) (int, config.State, []error) {
	state := ctx.Value("ctx").(config.ContextStruct).State
	provider := ctx.Value("ctx").(config.ContextStruct).Provider

	emitVerboseMetaInfo(ctx, "provider-sync", "GITHUB_SESSION_INIT", "initializing github session", map[string]interface{}{"provider": provider.Name})
	var gh *session.GithubSession
	gh, err := session.InitGithubSession()
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_SESSION_INIT_FAILED", "failed to initialize github session", map[string]interface{}{"provider": provider.Name, "error": err.Error()})
		return 0, state, []error{err}
	}

	var errs []error
	repos, err := getRepos(gh, provider.Owner)
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_FETCH_FAILED", "failed to fetch github repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "error": err.Error()})
		return 0, state, []error{err}
	}
	emitVerboseMetaInfo(ctx, "provider-sync", "PROVIDER_FETCHED", "fetched github repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "count": len(repos)})

	var wg sync.WaitGroup
	results := make(chan config.RepoState, len(repos))
	errorsChan := make(chan error, len(repos))

	for _, repo := range repos {
		wg.Add(1)
		go func(repo *github.Repository) {
			defer wg.Done()
			repoState, err := handleRepoCloneOnly(ctx, repo)
			if err != nil {
				errorsChan <- err
				return
			}
			results <- repoState
		}(repo)
	}

	go func() {
		wg.Wait()
		close(results)
		close(errorsChan)
	}()

	repoStates, repoErrs := collectRepoStateAndErrors(results, errorsChan)
	errs = append(errs, repoErrs...)
	for _, result := range repoStates {
		state = state.SaveRepoState(result, false)
	}

	return len(repos), state, errs
}

// getRepos lists every repository the token can see for owner. Organisations go
// through ListByOrg with type=all so private repos are included; the user
// endpoint ignores visibility for anyone but the authenticated user, so it is
// only used as a fallback when owner is not an organisation.
func getRepos(gh *session.GithubSession, org string) ([]*github.Repository, error) {
	orgOpts := github.RepositoryListByOrgOptions{
		Type:        "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var all []*github.Repository
	for {
		repos, resp, err := gh.Client.Repositories.ListByOrg(context.Background(), org, &orgOpts)
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound && len(all) == 0 {
				return getUserRepos(gh, org)
			}
			return nil, err
		}
		all = append(all, repos...)
		if resp.NextPage == 0 {
			break
		}
		orgOpts.Page = resp.NextPage
	}
	return all, nil
}

func getUserRepos(gh *session.GithubSession, user string) ([]*github.Repository, error) {
	opts := github.RepositoryListOptions{
		Visibility:  "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var all []*github.Repository
	for {
		repos, resp, err := gh.Client.Repositories.List(context.Background(), user, &opts)
		if err != nil {
			return nil, err
		}
		all = append(all, repos...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

func handleRepo(ctx context.Context, gh *session.GithubSession, repo *github.Repository) (config.RepoState, error) {
	contextStruct := ctx.Value("ctx").(config.ContextStruct)
	state := contextStruct.State
	provider := contextStruct.Provider
	cfg := contextStruct.Config

	isNew := false
	repoState := config.RepoState{
		Name:     repo.GetOwner().GetLogin() + "/" + repo.GetName(),
		CloneUrl: repo.GetSSHURL(),
		Public:   repo.GetVisibility() == "public",
		Driver:   provider.Name,
	}

	if !state.RepoInState(repoState.Name) {
		isNew = true
		go EvaluateRepoWatchers(ctx, RepoChange{
			Provider:   provider,
			RepoName:   repo.GetOwner().GetLogin() + "/" + repo.GetName(),
			Visibility: string(repo.GetVisibility()),
			Operation:  "ADD",
		})
	}

	clonePath := cfg.ClonePath
	if clonePath == "" {
		clonePath = "/tmp/nosygit/"
	}
	head, err := cloneIfNeeded(ctx, repoState.Name, repoState.CloneUrl, clonePath)
	if err != nil {
		return config.RepoState{}, fmt.Errorf("couldn't clone %s: %w", repoState.Name, err)
	}

	if contextStruct.Store != nil {
		if err := contextStruct.Store.UpsertRepo(storage.RepoUpsertInput{
			Driver:   repoState.Driver,
			Name:     repoState.Name,
			CloneURL: repoState.CloneUrl,
			Public:   repoState.Public,
			Head:     head,
		}); err != nil {
			events.EmitMetaWarning(ctx, "change-request-sync", "REPO_UPSERT_FAILED", "failed to ensure repo state before change request sync", map[string]interface{}{
				"provider": provider.Name,
				"repo":     repoState.Name,
				"error":    err.Error(),
			})
		} else if err := syncGithubPullRequests(ctx, gh, repoState, repo.GetOwner().GetLogin(), repo.GetName()); err != nil {
			events.EmitMetaWarning(ctx, "change-request-sync", "PROVIDER_FETCH_FAILED", "failed to sync github pull requests", map[string]interface{}{
				"provider": provider.Name,
				"repo":     repoState.Name,
				"error":    err.Error(),
			})
		}
	}

	if isNew == true {
		// For first discovery, keep Head empty so the same run can process current head commit.
		repoState.Head = ""
		return repoState, nil
	}
	repoState.Head = head

	var storedRepoState config.RepoState
	storedRepoState, err = state.GetRepoStateByName(repoState.Name)
	if err != nil || storedRepoState.Public == repoState.Public {
		return repoState, err
	}

	go EvaluateRepoVisibilityWatchers(ctx, RepoVisibilityChange{
		Provider:       provider,
		RepoName:       repoState.Name,
		FromVisibility: storedRepoState.GetPublicString(),
		ToVisibility:   repoState.GetPublicString(),
		Operation:      "EDIT",
	})

	return repoState, nil
}

func syncGithubPullRequests(ctx context.Context, gh *session.GithubSession, repoState config.RepoState, owner string, repo string) error {
	if gh == nil || gh.Client == nil {
		return errors.New("github client not initialized")
	}

	opts := &github.PullRequestListOptions{
		State: "all",
		Sort:  "updated",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	now := time.Now().UTC()
	for {
		pulls, res, err := gh.Client.PullRequests.List(ctx, owner, repo, opts)
		if err != nil {
			return err
		}

		for _, pr := range pulls {
			if pr == nil {
				continue
			}

			number := pr.GetNumber()
			numberPtr := &number
			externalID := strconv.FormatInt(pr.GetID(), 10)
			if externalID == "0" {
				externalID = ""
			}

			var openedAt *time.Time
			if pr.CreatedAt != nil {
				t := *pr.CreatedAt
				openedAt = &t
			}
			var updatedAt *time.Time
			if pr.UpdatedAt != nil {
				t := *pr.UpdatedAt
				updatedAt = &t
			}
			var closedAt *time.Time
			if pr.ClosedAt != nil {
				t := *pr.ClosedAt
				closedAt = &t
			}
			var mergedAt *time.Time
			if pr.MergedAt != nil {
				t := *pr.MergedAt
				mergedAt = &t
			}

			state := strings.TrimSpace(pr.GetState())
			if mergedAt != nil {
				state = "merged"
			}

			author := ""
			authorID := ""
			if pr.User != nil {
				author = pr.User.GetLogin()
				if id := pr.User.GetID(); id != 0 {
					authorID = strconv.FormatInt(id, 10)
				}
			}

			_, upsertErr := UpsertChangeRequest(ctx, storage.ChangeRequestInput{
				Repo: storage.RepoRef{
					Driver:   repoState.Driver,
					Name:     repoState.Name,
					CloneURL: repoState.CloneUrl,
				},
				Provider:          repoState.Driver,
				ExternalID:        externalID,
				Number:            numberPtr,
				Kind:              "pull_request",
				Title:             pr.GetTitle(),
				Description:       pr.GetBody(),
				AuthorLogin:       author,
				AuthorID:          authorID,
				SourceBranch:      pr.GetHead().GetRef(),
				TargetBranch:      pr.GetBase().GetRef(),
				SourceSHA:         pr.GetHead().GetSHA(),
				CommentsCount:     intPtr(pr.GetComments() + pr.GetReviewComments()),
				State:             state,
				IsDraft:           pr.GetDraft(),
				OpenedAt:          openedAt,
				ClosedAt:          closedAt,
				MergedAt:          mergedAt,
				UpdatedAtProvider: updatedAt,
				FirstSeenAt:       now,
				Source:            "poll",
			})
			if upsertErr != nil {
				return upsertErr
			}
		}

		if res == nil || res.NextPage == 0 {
			break
		}
		opts.Page = res.NextPage
	}

	return nil
}

func intPtr(v int) *int {
	out := v
	return &out
}

func handleRepoCloneOnly(ctx context.Context, repo *github.Repository) (config.RepoState, error) {
	contextStruct := ctx.Value("ctx").(config.ContextStruct)
	provider := contextStruct.Provider
	cfg := contextStruct.Config

	repoState := config.RepoState{
		Name:     repo.GetOwner().GetLogin() + "/" + repo.GetName(),
		CloneUrl: repo.GetSSHURL(),
		Public:   repo.GetVisibility() == "public",
		Driver:   provider.Name,
	}

	clonePath := cfg.ClonePath
	if clonePath == "" {
		clonePath = "/tmp/nosygit/"
	}
	head, err := cloneIfNeeded(ctx, repoState.Name, repoState.CloneUrl, clonePath)
	if err != nil {
		return config.RepoState{}, fmt.Errorf("couldn't clone %s: %w", repoState.Name, err)
	}
	repoState.Head = head

	return repoState, nil
}

// resolveSSHAuth builds credentials for git@ remotes from every source
// available: keys held by the SSH agent, SSH_KEY_PATH, then the default keys in
// ~/.ssh. All usable signers are offered together, so an empty or unrelated
// agent does not hide working key files. It returns an error describing each
// failed source when there are none, rather than continuing unauthenticated.
func buildSSHAuth() (transport.AuthMethod, io.Closer, error) {
	var signers []ssh.Signer
	var attempts []error
	var closer io.Closer

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		found, c, err := agentSigners(sock)
		if err != nil {
			attempts = append(attempts, fmt.Errorf("ssh agent: %w", err))
		}
		signers = append(signers, found...)
		closer = c
	}

	explicit := os.Getenv("SSH_KEY_PATH")
	var keyPaths []string
	if explicit != "" {
		keyPaths = append(keyPaths, explicit)
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			keyPaths = append(keyPaths, filepath.Join(home, ".ssh", name))
		}
	}
	for _, keyPath := range keyPaths {
		signer, err := loadSSHSigner(keyPath)
		if err != nil {
			// Missing default keys are expected; a missing explicit one is not.
			if errors.Is(err, os.ErrNotExist) && keyPath != explicit {
				continue
			}
			attempts = append(attempts, fmt.Errorf("%s: %w", keyPath, err))
			continue
		}
		signers = append(signers, signer)
	}

	if len(signers) == 0 {
		closeQuietly(closer)
		if len(attempts) == 0 {
			return nil, nil, errors.New("no ssh credentials found: start an ssh agent with a key loaded, set SSH_KEY_PATH, or add a key to ~/.ssh")
		}
		return nil, nil, fmt.Errorf("no usable ssh credentials: %w", errors.Join(attempts...))
	}

	hostKeyCallback, err := GitSSH.NewKnownHostsCallback()
	if err != nil {
		closeQuietly(closer)
		return nil, nil, fmt.Errorf("could not load known_hosts: %w", err)
	}
	return &GitSSH.PublicKeysCallback{
		User:                  "git",
		Callback:              func() ([]ssh.Signer, error) { return signers, nil },
		HostKeyCallbackHelper: GitSSH.HostKeyCallbackHelper{HostKeyCallback: hostKeyCallback},
	}, closer, nil
}

func closeQuietly(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
}

// agentSigners returns the signers held by the agent at sock, with the
// connection they use. The caller closes it when the signers are no longer needed.
func agentSigners(sock string) ([]ssh.Signer, io.Closer, error) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, err
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil || len(signers) == 0 {
		conn.Close()
		return nil, nil, err
	}
	return signers, conn, nil
}

func loadSSHSigner(privateSSHKeyFile string) (ssh.Signer, error) {
	sshKey, err := os.ReadFile(privateSSHKeyFile)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(sshKey)
	if err != nil {
		var passphraseErr *ssh.PassphraseMissingError
		if errors.As(err, &passphraseErr) {
			return nil, errors.New("key is passphrase-protected; load it into an ssh agent")
		}
		return nil, err
	}
	return signer, nil
}
