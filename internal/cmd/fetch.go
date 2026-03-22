package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/spf13/cobra"
	"nosygit/internal/config"
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

		var wg sync.WaitGroup
		for _, provider := range providers {
			wg.Add(1)
			go func(provider config.Provider) {
				defer wg.Done()

				ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
					Config:   cfg,
					Provider: provider,
					State:    state,
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
					repoCount = 1
				default:
					log.Printf("warning: provider %q has unsupported driver %q, skipping", provider.Name, provider.Driver)
					return
				}

				for _, e := range errs {
					log.Printf("warning: %s: %s", provider.Name, e)
				}
				log.Printf("fetched %d repositories from %s (%s)", repoCount, provider.Name, provider.Driver)
				state = newState
			}(provider)
		}
		wg.Wait()

		if err := state.WriteState(statePath); err != nil {
			log.Printf("warning: failed to write state: %s", err)
		}
	},
}
