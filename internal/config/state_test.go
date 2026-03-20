package config

import "testing"

func TestSaveRepoStatePreservesContributorsOnMetadataRefresh(t *testing.T) {
	state := State{
		Repos: []RepoState{
			{
				Name:     "acme/repo",
				CloneUrl: "git@github.com:acme/repo.git",
				Head:     "old-head",
				Contributors: map[string]ContributorState{
					"email:alice@example.com": {
						Name:    "Alice",
						Email:   "alice@example.com",
						Commits: 2,
					},
				},
			},
		},
	}

	refreshed := RepoState{
		Name:     "acme/repo",
		CloneUrl: "git@github.com:acme/repo.git",
		Head:     "new-head",
	}

	updated := state.SaveRepoState(refreshed, false)
	repo := updated.Repos[0]

	if repo.Head != "old-head" {
		t.Fatalf("expected old head to be preserved, got %s", repo.Head)
	}
	if len(repo.Contributors) != 1 {
		t.Fatalf("expected contributors to be preserved")
	}
}
