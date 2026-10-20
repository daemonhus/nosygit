package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"nosygit/internal/config"
	"nosygit/internal/events"
)

// repoNameFromURL derives an owner/repo name from a git remote URL.
// Supports HTTPS (https://host/org/repo.git) and SSH (git@host:org/repo.git) forms.
func repoNameFromURL(rawURL string) (string, error) {
	if strings.HasPrefix(rawURL, "git@") {
		parts := strings.SplitN(rawURL, ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid SSH URL: %s", rawURL)
		}
		name := strings.TrimSuffix(parts[1], ".git")
		if name == "" {
			return "", fmt.Errorf("could not derive repo name from URL: %s", rawURL)
		}
		return name, nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	name := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	if name == "" {
		return "", fmt.Errorf("could not derive repo name from URL: %s", rawURL)
	}
	return name, nil
}

// RunGitProvider clones the configured URL if needed, registers the repo in state,
// and returns the updated state. It does not run watcher evaluation.
func RunGitProvider(ctx context.Context) (config.State, error) {
	ctxValue := ctx.Value("ctx").(config.ContextStruct)
	state := ctxValue.State
	provider := ctxValue.Provider
	cfg := ctxValue.Config

	if provider.URL == "" {
		return state, fmt.Errorf("provider %q: url is required for git driver", provider.Name)
	}

	name, err := repoNameFromURL(provider.URL)
	if err != nil {
		return state, fmt.Errorf("provider %q: %w", provider.Name, err)
	}

	clonePath := providerClonePath(cfg, provider)

	isNew := !state.RepoInState(name)

	head, err := cloneIfNeeded(ctx, name, provider.URL, clonePath)
	if err != nil {
		return state, fmt.Errorf("provider %q: couldn't clone %s: %w", provider.Name, name, err)
	}

	repoState := config.RepoState{
		Name:     name,
		CloneUrl: provider.URL,
		Driver:   provider.Name,
	}
	if isNew {
		// Leave Head empty so the same run can process the current head commit.
		repoState.Head = ""
	} else {
		repoState.Head = head
	}

	state = state.SaveRepoState(repoState, false)
	return state, nil
}

// RunGitProviderCloneOnly clones the configured URL if needed and returns the updated
// state without running watcher evaluation. Used by nosygit fetch.
func RunGitProviderCloneOnly(ctx context.Context) (config.State, []error) {
	ctxValue := ctx.Value("ctx").(config.ContextStruct)
	state := ctxValue.State
	provider := ctxValue.Provider
	cfg := ctxValue.Config

	if provider.URL == "" {
		return state, []error{fmt.Errorf("provider %q: url is required for git driver", provider.Name)}
	}

	name, err := repoNameFromURL(provider.URL)
	if err != nil {
		return state, []error{fmt.Errorf("provider %q: %w", provider.Name, err)}
	}

	clonePath := providerClonePath(cfg, provider)

	head, err := cloneIfNeeded(ctx, name, provider.URL, clonePath)
	if err != nil {
		events.EmitMetaError(ctx, "provider-sync", "PROVIDER_FETCH_FAILED", "failed to clone git provider repository", map[string]interface{}{
			"provider": provider.Name,
			"url":      provider.URL,
			"error":    err.Error(),
		})
		return state, []error{fmt.Errorf("couldn't clone %s: %w", name, err)}
	}

	repoState := config.RepoState{
		Name:     name,
		CloneUrl: provider.URL,
		Driver:   provider.Name,
		Head:     head,
	}
	state = state.SaveRepoState(repoState, false)
	return state, nil
}
