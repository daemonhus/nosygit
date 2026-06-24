package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/spf13/cobra"
	"nosygit/internal/config"
	"nosygit/internal/storage"
)

func init() {
	rootCmd.AddCommand(fetchCmd)
}

var fetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Clone or pull repositories from providers without running watchers.",
	Long:  "Discovers and clones (or pulls) repositories from configured providers without running watchers or emitting events. Use --provider to target one provider or --all for all providers.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if !allProviders && providerName == "" {
			fmt.Fprintln(os.Stderr, "error: specify --all or --provider <name>")
			_ = cmd.Usage()
			return
		}
		if allProviders && providerName != "" {
			fmt.Fprintln(os.Stderr, "error: --all and --provider are mutually exclusive")
			_ = cmd.Usage()
			return
		}

		cfg, err := config.OpenConfig("./config.yml")
		if err != nil {
			log.Fatalf("failed to open config: %s", err)
		}
		state, err := config.OpenState(statePath)
		if err != nil {
			log.Fatalf("failed to open state: %s", err)
		}
		store, err := openStore(statePath)
		if err != nil {
			log.Fatalf("failed to open store: %s", err)
		}

		var providers []config.Provider
		if allProviders {
			for _, p := range cfg.Providers {
				providers = append(providers, p)
			}
		} else {
			p, ok := cfg.GetProvider(providerName)
			if !ok {
				log.Fatalf("error: provider %q not found in config", providerName)
			}
			providers = []config.Provider{p}
		}

		state, failed := fetchProviders(cfg, providers, state, store)

		if err := state.WriteState(statePath); err != nil {
			log.Printf("warning: failed to write state: %s", err)
		}

		if failed > 0 {
			log.Printf("fetch failed for %d of %d providers", failed, len(providers))
			os.Exit(1)
		}
	},
}

// fetchProviders clones (or pulls) repositories for each provider concurrently
// and returns the updated state along with the number of providers that
// reported at least one error. It performs no process-level I/O (no state
// write, no os.Exit), so callers control exit behavior and it stays testable.
func fetchProviders(cfg config.Config, providers []config.Provider, state config.State, store *storage.Store) (config.State, int) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed int
	for _, provider := range providers {
		wg.Add(1)
		go func(provider config.Provider) {
			defer wg.Done()

			mu.Lock()
			currentState := state
			mu.Unlock()

			ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
				Config:   cfg,
				Provider: provider,
				State:    currentState,
				Store:    store,
				Verbose:  verbose,
			})

			var repoCount int
			var newState config.State
			var errs []error

			switch provider.Driver {
			case "github":
				repoCount, newState, errs = RunGithubCloneOnly(ctx)
			case "gitlab":
				repoCount, newState, errs = RunGitlabCloneOnly(ctx)
			case "git":
				newState, errs = RunGitProviderCloneOnly(ctx)
				if len(errs) == 0 {
					repoCount = 1
				}
			default:
				log.Printf("warning: provider %q has unsupported driver %q, skipping", provider.Name, provider.Driver)
				return
			}

			for _, e := range errs {
				log.Printf("error: %s: %s", provider.Name, e)
			}

			mu.Lock()
			if len(errs) > 0 {
				failed++
			}
			state = newState
			mu.Unlock()

			log.Printf("fetched %d repositories from %s (%s)", repoCount, provider.Name, provider.Driver)
		}(provider)
	}
	wg.Wait()

	return state, failed
}
