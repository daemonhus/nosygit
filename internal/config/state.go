package config

import (
	"errors"
	"nosygit/internal/storage"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"os"
)

type RepoState struct {
	Name         string                      `yaml:"name"`
	CloneUrl     string                      `yaml:"clone_url"`
	Public       bool                        `yaml:"public"`
	Head         string                      `yaml:"head"`
	Driver       string                      `yaml:"driver"`
	Contributors map[string]ContributorState `yaml:"contributors,omitempty"`
}

type ContributorState struct {
	Name      string    `yaml:"name"`
	Email     string    `yaml:"email"`
	FirstSeen time.Time `yaml:"first_seen"`
	LastSeen  time.Time `yaml:"last_seen"`
	Commits   int       `yaml:"commits"`
}
type StateMeta struct {
	LastRun time.Time `yaml:"last_run"`
}
type State struct {
	Meta  StateMeta   `yaml:"meta"`
	Repos []RepoState `yaml:"repos"`
}

func (state State) RepoInState(name string) bool {
	for _, member := range state.Repos {
		if member.Name == name {
			return true
		}
	}
	return false
}

func (state State) GetRepoStateByName(repoName string) (RepoState, error) {
	for _, repoState := range state.Repos {
		if repoState.Name == repoName {
			return repoState, nil
		}
	}
	return RepoState{}, errors.New("no such repo in state")
}

func (repoState RepoState) GetPublicString() string {
	if repoState.Public {
		return "Public"
	}
	return "Private"
}

func OpenState(path string) (state State, err error) {
	if isYAMLPath(path) {
		return openYAMLState(path)
	}

	importLegacy := shouldImportLegacyYAML(path)
	store, err := storage.Open(path)
	if err != nil {
		return State{}, err
	}

	if importLegacy {
		legacyPath := legacyYAMLPath(path)
		legacyState, legacyErr := openYAMLState(legacyPath)
		if legacyErr == nil {
			if err := legacyState.WriteState(path); err == nil {
				return legacyState, nil
			}
		}
	}

	snapshot, err := store.LoadSnapshot()
	if err != nil {
		return State{}, err
	}

	state.Meta = StateMeta{LastRun: snapshot.LastRun}
	state.Repos = make([]RepoState, 0, len(snapshot.Repos))
	for _, repo := range snapshot.Repos {
		contributors := map[string]ContributorState{}
		for key, c := range repo.Contributors {
			contributors[key] = ContributorState{
				Name:      c.Name,
				Email:     c.Email,
				FirstSeen: c.FirstSeen,
				LastSeen:  c.LastSeen,
				Commits:   c.Commits,
			}
		}

		state.Repos = append(state.Repos, RepoState{
			Name:         repo.Name,
			CloneUrl:     repo.CloneURL,
			Public:       repo.Public,
			Head:         repo.Head,
			Driver:       repo.Driver,
			Contributors: contributors,
		})
	}

	return state, nil
}

func (state State) SaveRepoState(repoState RepoState, updateHead bool) State {
	newRepos := []RepoState{}
	added := false
	for _, repo := range state.Repos {
		if repo.CloneUrl != repoState.CloneUrl {
			newRepos = append(newRepos, repo)
			continue
		}

		// Repo in state, but we haven't run comparison yet
		if updateHead == false {
			repoState.Head = repo.Head
		}
		if len(repoState.Contributors) == 0 {
			repoState.Contributors = repo.Contributors
		}

		if repoState.Name != "" {
			newRepos = append(newRepos, repoState)
		}
		added = true
	}

	// New repo entirely
	if added == false {
		newRepos = append(newRepos, repoState)
	}
	state.Repos = newRepos
	return state
}

func (state State) WriteState(path string) error {
	if isYAMLPath(path) {
		return writeYAMLState(path, state)
	}

	store, err := storage.Open(path)
	if err != nil {
		return err
	}

	repos := make([]storage.RepoSnapshot, 0, len(state.Repos))
	for _, repo := range state.Repos {
		contributors := map[string]storage.ContributorSnapshot{}
		for key, c := range repo.Contributors {
			contributors[key] = storage.ContributorSnapshot{
				Name:      c.Name,
				Email:     c.Email,
				FirstSeen: c.FirstSeen,
				LastSeen:  c.LastSeen,
				Commits:   c.Commits,
			}
		}

		repos = append(repos, storage.RepoSnapshot{
			Name:         repo.Name,
			CloneURL:     repo.CloneUrl,
			Public:       repo.Public,
			Head:         repo.Head,
			Driver:       repo.Driver,
			Contributors: contributors,
		})
	}

	return store.SaveSnapshot(storage.Snapshot{
		LastRun: state.Meta.LastRun,
		Repos:   repos,
	})
}

func isYAMLPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yml" || ext == ".yaml"
}

func legacyYAMLPath(path string) string {
	return filepath.Join(filepath.Dir(path), "state.yml")
}

func shouldImportLegacyYAML(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return false
	}
	legacy := legacyYAMLPath(path)
	if _, err := os.Stat(legacy); err == nil {
		return true
	}
	return false
}

func openYAMLState(path string) (State, error) {
	stateBytes, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}

	var state State
	err = yaml.Unmarshal(stateBytes, &state)
	return state, err
}

func writeYAMLState(path string, state State) error {
	file, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, file, 0o644)
}
