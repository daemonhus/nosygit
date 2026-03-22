package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStateSQLiteEmpty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	state, err := OpenState(dbPath)
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	if len(state.Repos) != 0 {
		t.Fatalf("expected empty repo list")
	}
}

func TestWriteAndReadSQLiteState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	now := time.Now().UTC().Truncate(time.Second)
	in := State{
		Meta: StateMeta{LastRun: now},
		Repos: []RepoState{
			{
				Name:     "acme/repo",
				CloneUrl: "git@example.com:acme/repo.git",
				Public:   true,
				Head:     "deadbeef",
				Driver:   "gh",
				Contributors: map[string]ContributorState{
					"email:alice@example.com": {
						Name:      "Alice",
						Email:     "alice@example.com",
						FirstSeen: now,
						LastSeen:  now,
						Commits:   3,
					},
				},
			},
		},
	}

	if err := in.WriteState(dbPath); err != nil {
		t.Fatalf("write state: %v", err)
	}

	out, err := OpenState(dbPath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if len(out.Repos) != 1 {
		t.Fatalf("expected one repo, got %d", len(out.Repos))
	}
	repo := out.Repos[0]
	if repo.Name != "acme/repo" || repo.CloneUrl != "git@example.com:acme/repo.git" || repo.Head != "deadbeef" {
		t.Fatalf("unexpected repo roundtrip: %+v", repo)
	}
	if len(repo.Contributors) != 1 {
		t.Fatalf("expected one contributor")
	}
}

func TestOpenStateImportsLegacyYAML(t *testing.T) {
	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "state.yml")
	dbPath := filepath.Join(tmp, "state.db")

	content := `meta:
  last_run: 2026-02-14T00:00:00Z
repos:
  - name: acme/repo
    clone_url: git@example.com:acme/repo.git
    public: true
    head: cafebabe
    driver: gh
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write legacy yaml: %v", err)
	}

	imported, err := OpenState(dbPath)
	if err != nil {
		t.Fatalf("open state with import: %v", err)
	}
	if len(imported.Repos) != 1 {
		t.Fatalf("expected import to include one repo")
	}

	if err := os.Remove(yamlPath); err != nil {
		t.Fatalf("remove legacy yaml: %v", err)
	}

	fromDB, err := OpenState(dbPath)
	if err != nil {
		t.Fatalf("open state from db: %v", err)
	}
	if len(fromDB.Repos) != 1 || fromDB.Repos[0].Head != "cafebabe" {
		t.Fatalf("expected imported data to persist in sqlite")
	}
}
