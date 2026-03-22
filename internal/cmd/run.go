package cmd

import "github.com/spf13/cobra"

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a nosygit service (poll or listen).",
	Long:  "Run a nosygit service: 'run poll' to fetch and evaluate commits on a schedule, 'run listen' to start a webhook server.",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
