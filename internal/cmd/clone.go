package cmd

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"log"
	"nosygit/internal/config"
	"sync"
)

func init() {
	rootCmd.AddCommand(cloneCmd)
	cloneCmd.Flags().BoolP("all", "a", false, "Clone from all configured providers")
	cloneCmd.Flags().StringP("provider", "p", "", "Clone from specific provider")
}

var cloneCmd = &cobra.Command{
	Use:   "clone",
	Short: "Clone repositories without running watchers",
	Long:  "Discovers and clones repositories from configured providers without running watchers or events",
	Run: func(cmd *cobra.Command, args []string) {
		PrintBanner()

		all, _ := cmd.Flags().GetBool("all")
		providerName, _ := cmd.Flags().GetString("provider")

		if !all && providerName == "" {
			fmt.Println("❌ Please specify --all or --provider [name]")
			return
		}

		if all && providerName != "" {
			fmt.Println("❌ Cannot use --all and --provider together")
			return
		}

		clone(all, providerName)
	},
}

func clone(all bool, providerName string) {
	cfg, err := config.OpenConfig("./config.yml")
	if err != nil {
		log.Fatal(err.Error())
	}

	state, err := config.OpenState(statePath)
	if err != nil {
		log.Fatal(err.Error())
	}
	store, err := openStore(statePath)
	if err != nil {
		log.Fatal(err.Error())
	}

	var providers []config.Provider

	if all {
		for _, provider := range cfg.Providers {
			providers = append(providers, provider)
		}
		fmt.Printf("🔄 Cloning from all %d configured providers...\n", len(providers))
	} else {
		provider, exists := cfg.GetProvider(providerName)
		if !exists {
			log.Fatalf("❌ Provider '%s' not found in config", providerName)
		}
		providers = []config.Provider{provider}
		fmt.Printf("🔄 Cloning from provider: %s\n", providerName)
	}

	var wg sync.WaitGroup
	totalRepos := 0

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
			default:
				fmt.Printf("⚠️  Unsupported provider driver: %s\n", provider.Driver)
				return
			}

			if len(errs) > 0 {
				fmt.Printf("⚠️  Errors from %s:\n", provider.Name)
				for _, err := range errs {
					fmt.Printf("   %s\n", err.Error())
				}
			}

			totalRepos += repoCount
			state = newState
			fmt.Printf("✅ Cloned %d repositories from %s (%s)\n", repoCount, provider.Name, provider.Driver)
		}(provider)
	}

	wg.Wait()

	err = state.WriteState(statePath)
	if err != nil {
		log.Printf("⚠️  Failed to save state: %s", err.Error())
	}

	fmt.Printf("🎉 Successfully cloned %d total repositories\n", totalRepos)
}
