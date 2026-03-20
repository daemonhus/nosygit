package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"os"
)

var configPath = "./config.yml"
var statePath = "./state.db"
var verbose bool

var rootCmd = &cobra.Command{
	Use:   "nosygit",
	Short: "A daemon that watches Git repos and emits events based on observed changes.",
	Long:  "nosygit polls GitHub and GitLab providers, tracks repository and commit changes, and triggers configured watcher events.\n\nRequires a config.yml in the working directory. See docs/usage.md for setup and examples.",
	Run: func(cmd *cobra.Command, args []string) {
		PrintBanner()
		err := cmd.Help()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd:   true,
		DisableNoDescFlag:   false,
		DisableDescriptions: false,
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Emit verbose runtime meta events")
}
