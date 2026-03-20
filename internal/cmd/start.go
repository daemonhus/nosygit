package cmd

import (
	"context"
	"github.com/spf13/cobra"
	"log"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/logging"
	"sync"
	"time"
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the nosygit daemon: enumerate and pull any repos, run watchers against any new commits.",
	Long:  "Start the nosygit daemon: enumerate and pull any repos, run watchers against any new commits.",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		PrintBanner()
		start()
		//fmt.Println("💤 Waiting to poll...")
	},
}

func start() {
	configPath = "./config.yml"
	statePath = "./state.db"

	cfg, err := config.OpenConfig(configPath)
	if err != nil {
		log.Fatalf("☠️ Failed to open config: %s", err.Error())
	}

	state, err := config.OpenState(statePath)
	if err != nil {
		log.Fatalf("☠️ Failed to load state: %s", err.Error())
	}
	store, err := openStore(statePath)
	if err != nil {
		log.Fatalf("☠️ Failed to open state store: %s", err.Error())
	}

	cfg.PrintWatchers()
	baseCtx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:  cfg,
		State:   state,
		Store:   store,
		Verbose: verbose,
	})
	emitVerboseMetaInfo(baseCtx, "runner", "INIT", "initialized run context", map[string]interface{}{
		"config_path": configPath,
		"state_path":  statePath,
		"watchers":    len(cfg.Watchers),
		"providers":   len(cfg.Providers),
	})
	events.EmitMeta(baseCtx, logging.MetaInfo, "run-started", "runner", "RUN_STARTED", "start run", nil)

	providerPolls, err := store.LoadProviderPolls()
	if err != nil {
		log.Fatalf("☠️ Failed to load provider poll state: %s", err.Error())
	}
	emitVerboseMetaInfo(baseCtx, "poller", "PROVIDER_POLL_STATE_LOADED", "loaded provider poll state", map[string]interface{}{"providers_with_state": len(providerPolls)})

	now := time.Now()
	dueProviders := map[string]bool{}
	for _, provider := range cfg.Providers {
		due, err := isProviderDue(provider, providerPolls[provider.Name], now)
		if err != nil {
			log.Fatalf("☠️ Invalid poll interval for provider %s: %s", provider.Name, err.Error())
		}
		dueProviders[provider.Name] = due
	}

	var errs []error
	events.EmitMetaInfo(baseCtx, "poller", "PROVIDER_FETCH_START", "fetching repos from github providers", nil)
	cnt := 0
	for _, provider := range cfg.GetProviders("github") {
		if !dueProviders[provider.Name] {
			events.EmitMetaInfo(baseCtx, "poller", "PROVIDER_SKIPPED", "skipping provider due to poll interval", map[string]interface{}{"provider": provider.Name})
			continue
		}

		ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
			Config:   cfg,
			Provider: provider,
			State:    state,
			Store:    store,
			Verbose:  verbose,
		})
		emitVerboseMetaInfo(ctx, "poller", "PROVIDER_FETCH_PROVIDER_START", "fetching github provider repositories", map[string]interface{}{"provider": provider.Name})

		var orgCount int
		var githubErrs []error
		orgCount, state, githubErrs = RunGithub(ctx)
		errs = append(errs, githubErrs[:]...)
		if len(githubErrs) > 0 {
			events.EmitMetaError(ctx, "poller", "PROVIDER_FETCH_FAILED", "failed fetching github provider repositories", map[string]interface{}{"provider": provider.Name, "errors": len(githubErrs)})
		} else {
			emitVerboseMetaInfo(ctx, "poller", "PROVIDER_FETCH_PROVIDER_DONE", "finished fetching github provider repositories", map[string]interface{}{"provider": provider.Name, "fetched": orgCount})
		}
		cnt += orgCount

		if err := store.TouchProviderPoll(provider.Name, time.Now()); err != nil {
			events.EmitMetaWarning(ctx, "poller", "PROVIDER_POLL_TOUCH_FAILED", "failed updating provider poll timestamp", map[string]interface{}{"provider": provider.Name, "error": err.Error()})
		}
	}
	events.EmitMetaInfo(baseCtx, "poller", "PROVIDER_FETCH_DONE", "finished github provider fetch", map[string]interface{}{"fetched": cnt})

	events.EmitMetaInfo(baseCtx, "poller", "PROVIDER_FETCH_START", "fetching repos from gitlab providers", nil)
	cnt = 0
	for _, provider := range cfg.GetProviders("gitlab") {
		if !dueProviders[provider.Name] {
			events.EmitMetaInfo(baseCtx, "poller", "PROVIDER_SKIPPED", "skipping provider due to poll interval", map[string]interface{}{"provider": provider.Name})
			continue
		}

		ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
			Config:   cfg,
			Provider: provider,
			State:    state,
			Store:    store,
			Verbose:  verbose,
		})
		emitVerboseMetaInfo(ctx, "poller", "PROVIDER_FETCH_PROVIDER_START", "fetching gitlab provider repositories", map[string]interface{}{"provider": provider.Name})

		var orgCount int
		var gitlabErrs []error
		orgCount, state, gitlabErrs = RunGitlab(ctx)
		errs = append(errs, gitlabErrs[:]...)
		if len(gitlabErrs) > 0 {
			events.EmitMetaError(ctx, "poller", "PROVIDER_FETCH_FAILED", "failed fetching gitlab provider repositories", map[string]interface{}{"provider": provider.Name, "errors": len(gitlabErrs)})
		} else {
			emitVerboseMetaInfo(ctx, "poller", "PROVIDER_FETCH_PROVIDER_DONE", "finished fetching gitlab provider repositories", map[string]interface{}{"provider": provider.Name, "fetched": orgCount})
		}
		cnt += orgCount

		if err := store.TouchProviderPoll(provider.Name, time.Now()); err != nil {
			events.EmitMetaWarning(ctx, "poller", "PROVIDER_POLL_TOUCH_FAILED", "failed updating provider poll timestamp", map[string]interface{}{"provider": provider.Name, "error": err.Error()})
		}
	}
	events.EmitMetaInfo(baseCtx, "poller", "PROVIDER_FETCH_DONE", "finished gitlab provider fetch", map[string]interface{}{"fetched": cnt})

	if len(errs) != 0 {
		for _, err = range errs {
			events.EmitMetaError(baseCtx, "provider-sync", "PROVIDER_ERROR", "provider sync error", map[string]interface{}{"error": err.Error()})
		}
	}

	// Process repositories in parallel
	var wg sync.WaitGroup
	results := make(chan config.RepoState, len(state.Repos))
	errorsChan := make(chan error, len(state.Repos))

	for _, repoState := range state.Repos {
		var provider config.Provider
		var ok bool
		if provider, ok = cfg.GetProvider(repoState.Driver); !ok {
			events.EmitMetaWarning(baseCtx, "runner", "UNKNOWN_REPO_DRIVER", "repo in state has unknown driver", map[string]interface{}{"repo": repoState.Name, "driver": repoState.Driver})
			continue
		}
		if !dueProviders[provider.Name] {
			continue
		}

		wg.Add(1)
		go func(repoState config.RepoState, provider config.Provider) {
			defer wg.Done()

			ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
				Config:    cfg,
				Provider:  provider,
				State:     state,
				RepoState: repoState,
				Store:     store,
				Verbose:   verbose,
			})

			clonePath := cfg.ClonePath
			if clonePath == "" {
				clonePath = "/tmp/nosygit/"
			}
			updatedRepoState, err := RunGit(ctx, clonePath+repoState.Name, repoState)
			if err != nil {
				events.EmitMetaError(ctx, "runner", "REPO_PROCESS_FAILED", "failed processing repository", map[string]interface{}{"repo": repoState.Name, "error": err.Error()})
				errorsChan <- err
			} else {
				results <- updatedRepoState
			}
		}(repoState, provider)
	}

	// Close channels when all goroutines finish
	go func() {
		wg.Wait()
		close(results)
		close(errorsChan)
	}()

	// Collect errors concurrently
	var repoErrs []error
	done := make(chan bool)
	go func() {
		for err := range errorsChan {
			repoErrs = append(repoErrs, err)
		}
		done <- true
	}()

	// Collect results and update state
	for result := range results {
		state = state.SaveRepoState(result, true)
	}

	// Wait for error collection to finish
	<-done

	events.EmitMeta(baseCtx, logging.MetaInfo, "run-finished", "runner", "RUN_FINISHED", "finished run, updating state", map[string]interface{}{"repos": len(state.Repos)})
	state.Meta = config.StateMeta{LastRun: time.Now()}
	err = state.WriteState(statePath)
	if err != nil {
		events.EmitMetaError(baseCtx, "state", "STATE_WRITE_FAILED", "failed writing state", map[string]interface{}{"error": err.Error()})
	}
}

func isProviderDue(provider config.Provider, lastPolledAt time.Time, now time.Time) (bool, error) {
	interval, err := provider.PollInterval()
	if err != nil {
		return false, err
	}
	if interval == 0 {
		return true, nil
	}
	if lastPolledAt.IsZero() {
		return true, nil
	}
	return !now.Before(lastPolledAt.Add(interval)), nil
}
