package cmd

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"
)

var stateListProvider string
var stateListPrefix string

func init() {
	rootCmd.AddCommand(stateCmd)
	stateCmd.AddCommand(stateListCmd)
	stateListCmd.Flags().StringVarP(&stateListProvider, "provider", "p", "", "Filter by provider name")
	stateListCmd.Flags().StringVar(&stateListPrefix, "prefix", "", "Filter by repository path prefix")
}

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Inspect persisted repository state",
}

var stateListCmd = &cobra.Command{
	Use:   "list",
	Short: "List repositories with current head commit and message",
	Run: func(cmd *cobra.Command, args []string) {
		store, err := openStore(statePath)
		if err != nil {
			log.Fatal(err.Error())
		}

		type row struct {
			Provider string         `gorm:"column:provider"`
			Name     string         `gorm:"column:name"`
			Location string         `gorm:"column:location"`
			Head     string         `gorm:"column:head"`
			Message  sql.NullString `gorm:"column:message"`
		}

		var rows []row
		err = store.DB().Raw(`
			SELECT
				r.driver AS provider,
				r.name AS name,
				r.clone_url AS location,
				r.head AS head,
				COALESCE(NULLIF(c.message_title, ''), NULLIF(c.message, '')) AS message
			FROM repos r
			LEFT JOIN commits c ON c.repo_id = r.id AND c.sha = r.head
			ORDER BY r.driver ASC, r.name ASC
		`).Scan(&rows).Error
		if err != nil {
			log.Fatal(err.Error())
		}

		filtered := make([]row, 0, len(rows))
		for _, r := range rows {
			if stateListProvider != "" && r.Provider != stateListProvider {
				continue
			}
			if stateListPrefix != "" && !strings.HasPrefix(r.Name, stateListPrefix) {
				continue
			}
			filtered = append(filtered, r)
		}

		if len(filtered) == 0 {
			fmt.Println("No repositories found for current filter.")
			return
		}

		trows := make([]stateTableRow, 0, len(filtered))
		for _, r := range filtered {
			msg := "(no commit message)"
			if r.Message.Valid {
				if line := firstNonEmptyLine(r.Message.String); line != "" {
					msg = line
				}
			}
			trows = append(trows, stateTableRow{
				Provider: r.Provider,
				Head:     shortHead(r.Head),
				Repo:     r.Name,
				Location: r.Location,
				Message:  msg,
			})
		}

		printStateTable(trows)
	},
}

type stateTableRow struct {
	Provider string
	Head     string
	Repo     string
	Location string
	Message  string
}

func printStateTable(rows []stateTableRow) {
	headers := []string{"PROVIDER", "HEAD", "REPO", "LOCATION", "MESSAGE"}
	wProvider := len(headers[0])
	wHead := len(headers[1])
	wRepo := len(headers[2])
	wLocation := len(headers[3])
	wMessage := len(headers[4])

	for _, r := range rows {
		if len(r.Provider) > wProvider {
			wProvider = len(r.Provider)
		}
		if len(r.Head) > wHead {
			wHead = len(r.Head)
		}
		if len(r.Repo) > wRepo {
			wRepo = len(r.Repo)
		}
		if len(r.Location) > wLocation {
			wLocation = len(r.Location)
		}
		if len(r.Message) > wMessage {
			wMessage = len(r.Message)
		}
	}

	sep := "+" + strings.Repeat("-", wProvider+2) +
		"+" + strings.Repeat("-", wHead+2) +
		"+" + strings.Repeat("-", wRepo+2) +
		"+" + strings.Repeat("-", wLocation+2) +
		"+" + strings.Repeat("-", wMessage+2) + "+"

	fmt.Println(sep)
	fmt.Printf("| %-*s | %-*s | %-*s | %-*s | %-*s |\n",
		wProvider, headers[0], wHead, headers[1], wRepo, headers[2], wLocation, headers[3], wMessage, headers[4],
	)
	fmt.Println(sep)
	for _, r := range rows {
		fmt.Printf("| %-*s | %-*s | %-*s | %-*s | %-*s |\n",
			wProvider, r.Provider, wHead, r.Head, wRepo, r.Repo, wLocation, r.Location, wMessage, r.Message,
		)
	}
	fmt.Println(sep)
}

func shortHead(head string) string {
	h := strings.TrimSpace(head)
	if h == "" {
		return "(no-head)"
	}
	if len(h) <= 10 {
		return h
	}
	return h[:10]
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
