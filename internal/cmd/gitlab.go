package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/xanzy/go-gitlab"
	"net/http"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/session"
	"nosygit/internal/storage"
	"strconv"
	"strings"
	"sync"
	"time"
)

func RunGitlab(ctx context.Context) (int, config.State, []error) {
	state := ctx.Value("ctx").(config.ContextStruct).State
	provider := ctx.Value("ctx").(config.ContextStruct).Provider

	var errs []error
	emitVerboseMetaInfo(ctx, "provider-sync", "GITLAB_SESSION_INIT", "initializing gitlab session", map[string]interface{}{"provider": provider.Name})
	var gl *session.GitlabSession
	gl, err := session.InitGitlabSession()
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_SESSION_INIT_FAILED", "failed to initialize gitlab session", map[string]interface{}{"provider": provider.Name, "error": err.Error()})
		return 0, state, []error{err}
	}

	projects, err := getProjects(gl, provider.Owner)
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_FETCH_FAILED", "failed to fetch gitlab repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "error": err.Error()})
		return 0, state, []error{err}
	}
	emitVerboseMetaInfo(ctx, "provider-sync", "PROVIDER_FETCHED", "fetched gitlab repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "count": len(projects)})

	var wg sync.WaitGroup
	results := make(chan config.RepoState, len(projects))
	errorsChan := make(chan error, len(projects))

	for _, project := range projects {
		wg.Add(1)
		go func(project *gitlab.Project) {
			defer wg.Done()
			repoState, err := handleProject(ctx, gl, project)
			if err != nil {
				errorsChan <- fmt.Errorf("couldn't pull %s: %w", project.PathWithNamespace, err)
				return
			}
			results <- repoState
		}(project)
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

	return len(projects), state, errs
}

func RunGitlabCloneOnly(ctx context.Context) (int, config.State, []error) {
	state := ctx.Value("ctx").(config.ContextStruct).State
	provider := ctx.Value("ctx").(config.ContextStruct).Provider

	var errs []error
	emitVerboseMetaInfo(ctx, "provider-sync", "GITLAB_SESSION_INIT", "initializing gitlab session", map[string]interface{}{"provider": provider.Name})
	var gl *session.GitlabSession
	gl, err := session.InitGitlabSession()
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_SESSION_INIT_FAILED", "failed to initialize gitlab session", map[string]interface{}{"provider": provider.Name, "error": err.Error()})
		return 0, state, []error{err}
	}

	projects, err := getProjects(gl, provider.Owner)
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_FETCH_FAILED", "failed to fetch gitlab repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "error": err.Error()})
		return 0, state, []error{err}
	}
	emitVerboseMetaInfo(ctx, "provider-sync", "PROVIDER_FETCHED", "fetched gitlab repositories", map[string]interface{}{"provider": provider.Name, "owner": provider.Owner, "count": len(projects)})

	var wg sync.WaitGroup
	results := make(chan config.RepoState, len(projects))
	errorsChan := make(chan error, len(projects))

	for _, project := range projects {
		wg.Add(1)
		go func(project *gitlab.Project) {
			defer wg.Done()
			repoState, err := handleProjectCloneOnly(ctx, project)
			if err != nil {
				errorsChan <- fmt.Errorf("couldn't pull %s: %w", project.PathWithNamespace, err)
				return
			}
			results <- repoState
		}(project)
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

	return len(projects), state, errs
}

func getProjects(gl *session.GitlabSession, org string) (projects []*gitlab.Project, err error) {
	membership := true
	page := 1
	opt := &gitlab.ListProjectsOptions{
		Membership: &membership,
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
			Page:    page,
		},
	}

	for {
		current, res, err := gl.Client.Projects.ListProjects(opt)
		if err != nil {
			return nil, err
		}
		for _, p := range current {
			if strings.HasPrefix(p.PathWithNamespace, org) == false {
				continue
			}
			projects = append(projects, p)
		}

		if res.NextPage == 0 {
			break
		}

		opt.Page = res.NextPage
	}

	return projects, nil
}

func handleProject(ctx context.Context, gl *session.GitlabSession, project *gitlab.Project) (config.RepoState, error) {
	contextStruct := ctx.Value("ctx").(config.ContextStruct)
	state := contextStruct.State
	provider := contextStruct.Provider
	cfg := contextStruct.Config

	isNew := false
	repoState := config.RepoState{
		Name:     project.PathWithNamespace,
		CloneUrl: project.SSHURLToRepo,
		Public:   project.Visibility == "public",
		Driver:   provider.Name,
	}

	if !state.RepoInState(repoState.Name) {
		isNew = true
		go EvaluateRepoWatchers(ctx, RepoChange{
			Provider:   provider,
			RepoState:  repoState,
			RepoName:   project.PathWithNamespace,
			Visibility: string(project.Visibility),
			Operation:  "ADD",
		})
	}

	if state.RepoInState(repoState.Name) && project.Archived {
		go EvaluateRepoWatchers(ctx, RepoChange{
			Provider:   provider,
			RepoState:  repoState,
			RepoName:   project.PathWithNamespace,
			Visibility: string(project.Visibility),
			Operation:  "DELETE",
		})
	}

	if !isNew {
		var storedRepoState config.RepoState
		storedRepoState, err := state.GetRepoStateByName(repoState.Name)
		if err != nil {
			return repoState, err
		}

		if storedRepoState.Public != repoState.Public {
			go EvaluateRepoVisibilityWatchers(ctx, RepoVisibilityChange{
				Provider:       provider,
				RepoState:      repoState,
				RepoName:       repoState.Name,
				FromVisibility: storedRepoState.GetPublicString(),
				ToVisibility:   repoState.GetPublicString(),
				Operation:      "EDIT",
			})
		}
	}

	if project.EmptyRepo {
		return repoState, nil
	}

	clonePath := providerClonePath(cfg, provider)
	head, err := cloneIfNeeded(ctx, repoState.Name, repoState.CloneUrl, clonePath)
	if err != nil {
		return repoState, fmt.Errorf("couldn't clone: %w", err)
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
		} else if err := syncGitlabMergeRequests(ctx, gl, repoState, project.ID); errors.Is(err, errMergeRequestsForbidden) {
			emitVerboseMetaInfo(ctx, "change-request-sync", "MERGE_REQUESTS_SKIPPED", "skipping merge requests the token cannot access", map[string]interface{}{
				"provider": provider.Name,
				"repo":     repoState.Name,
			})
		} else if err != nil {
			events.EmitMetaWarning(ctx, "change-request-sync", "PROVIDER_FETCH_FAILED", "failed to sync gitlab merge requests", map[string]interface{}{
				"provider": provider.Name,
				"repo":     repoState.Name,
				"error":    err.Error(),
			})
		}
	}

	if isNew {
		// For first discovery, keep Head empty so the same run can process current head commit.
		repoState.Head = ""
		return repoState, nil
	}

	repoState.Head = head
	return repoState, nil
}

// errMergeRequestsForbidden marks a 403 on a project's merge requests, which
// happens when they are disabled or hidden from the token and is not a failure.
var errMergeRequestsForbidden = errors.New("merge requests not accessible")

func syncGitlabMergeRequests(ctx context.Context, gl *session.GitlabSession, repoState config.RepoState, projectID int) error {
	if gl == nil || gl.Client == nil {
		return errors.New("gitlab client not initialized")
	}

	state := "all"
	orderBy := "updated_at"
	sortDir := "desc"
	opts := &gitlab.ListProjectMergeRequestsOptions{
		State:   &state,
		OrderBy: &orderBy,
		Sort:    &sortDir,
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
		},
	}

	now := time.Now().UTC()
	for {
		mrs, res, err := gl.Client.MergeRequests.ListProjectMergeRequests(projectID, opts)
		if err != nil {
			if res != nil && res.StatusCode == http.StatusForbidden {
				return errMergeRequestsForbidden
			}
			return err
		}

		for _, mr := range mrs {
			if mr == nil {
				continue
			}

			number := mr.IID
			numberPtr := &number
			externalID := strconv.Itoa(mr.ID)
			if mr.ID == 0 {
				externalID = ""
			}

			authorLogin := ""
			authorID := ""
			if mr.Author != nil {
				authorLogin = mr.Author.Username
				if mr.Author.ID != 0 {
					authorID = strconv.Itoa(mr.Author.ID)
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
				Kind:              "merge_request",
				Title:             mr.Title,
				Description:       mr.Description,
				AuthorLogin:       authorLogin,
				AuthorID:          authorID,
				SourceBranch:      mr.SourceBranch,
				TargetBranch:      mr.TargetBranch,
				SourceSHA:         mr.SHA,
				CommentsCount:     intPtr(mr.UserNotesCount),
				State:             mr.State,
				IsDraft:           mr.WorkInProgress,
				OpenedAt:          mr.CreatedAt,
				ClosedAt:          mr.ClosedAt,
				MergedAt:          mr.MergedAt,
				UpdatedAtProvider: mr.UpdatedAt,
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

func handleProjectCloneOnly(ctx context.Context, project *gitlab.Project) (config.RepoState, error) {
	contextStruct := ctx.Value("ctx").(config.ContextStruct)
	provider := contextStruct.Provider
	cfg := contextStruct.Config

	repoState := config.RepoState{
		Name:     project.PathWithNamespace,
		CloneUrl: project.SSHURLToRepo,
		Public:   project.Visibility == "public",
		Driver:   provider.Name,
	}

	if project.EmptyRepo {
		return repoState, nil
	}

	clonePath := providerClonePath(cfg, provider)
	head, err := cloneIfNeeded(ctx, repoState.Name, repoState.CloneUrl, clonePath)
	if err != nil {
		return repoState, fmt.Errorf("couldn't clone: %w", err)
	}

	repoState.Head = head
	return repoState, nil
}
