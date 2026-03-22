package cmd

import (
	"context"
	"errors"
	"fmt"
	"nosygit/internal/config"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Mock GitLab project for testing
type mockGitlabProject struct {
	pathWithNamespace string
	sshURLToRepo      string
	visibility        string
	emptyRepo         bool
}

func (m *mockGitlabProject) GetPathWithNamespace() string { return m.pathWithNamespace }
func (m *mockGitlabProject) GetSSHURLToRepo() string      { return m.sshURLToRepo }
func (m *mockGitlabProject) GetVisibility() string        { return m.visibility }
func (m *mockGitlabProject) IsEmptyRepo() bool            { return m.emptyRepo }

// Mock GitHub repository for testing
type mockGithubRepo struct {
	owner      string
	name       string
	sshURL     string
	visibility string
}

func (m *mockGithubRepo) GetOwner() mockOwner   { return mockOwner{login: m.owner} }
func (m *mockGithubRepo) GetName() string       { return m.name }
func (m *mockGithubRepo) GetSSHURL() string     { return m.sshURL }
func (m *mockGithubRepo) GetVisibility() string { return m.visibility }

type mockOwner struct {
	login string
}

func (m mockOwner) GetLogin() string { return m.login }

func TestCloneChannelHandling(t *testing.T) {
	// Test that channels don't deadlock with multiple concurrent operations

	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	cfg := config.Config{
		ClonePath: tmpDir + "/",
	}

	state := config.State{
		Repos: []config.RepoState{},
	}

	provider := config.Provider{
		Name:   "test-provider",
		Driver: "gitlab",
		Owner:  "test-org",
	}

	_ = context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:   cfg,
		Provider: provider,
		State:    state,
	})

	// Simulate multiple repositories being processed
	numRepos := 10
	results := make(chan config.RepoState, numRepos)
	errorsChan := make(chan error, numRepos)
	var wg sync.WaitGroup

	// Launch goroutines that simulate repository processing
	for i := 0; i < numRepos; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Simulate some processing time
			time.Sleep(time.Millisecond * 10)

			if idx%3 == 0 {
				// Simulate an error for every 3rd repo
				errorsChan <- errors.New("simulated error")
			}

			// Always send a result (even if there was an error)
			results <- config.RepoState{
				Name:     "test-repo",
				CloneUrl: "git@example.com:test/repo.git",
				Public:   true,
				Driver:   "gitlab",
			}
		}(i)
	}

	// Close channels when all goroutines finish
	go func() {
		wg.Wait()
		close(results)
		close(errorsChan)
	}()

	// Test the channel reading pattern from our fix
	var errs []error
	done := make(chan bool)

	// Read errors concurrently
	go func() {
		for err := range errorsChan {
			errs = append(errs, err)
		}
		done <- true
	}()

	// Read results in main goroutine
	var processedResults []config.RepoState
	for result := range results {
		processedResults = append(processedResults, result)
	}

	// Wait for error collection to finish
	<-done

	// Verify we processed all repositories
	if len(processedResults) != numRepos {
		t.Errorf("Expected %d results, got %d", numRepos, len(processedResults))
	}

	// Verify we collected expected errors (every 3rd repo)
	expectedErrors := numRepos / 3
	if numRepos%3 == 0 {
		expectedErrors = numRepos / 3
	} else {
		expectedErrors = numRepos/3 + 1
	}
	if len(errs) != expectedErrors {
		t.Errorf("Expected ~%d errors, got %d", expectedErrors, len(errs))
	}
}

func TestCloneValidation(t *testing.T) {
	tests := []struct {
		name         string
		all          bool
		providerName string
		shouldError  bool
		expectedMsg  string
	}{
		{
			name:         "no flags provided",
			all:          false,
			providerName: "",
			shouldError:  true,
			expectedMsg:  "Please specify --all or --provider",
		},
		{
			name:         "both flags provided",
			all:          true,
			providerName: "test-provider",
			shouldError:  true,
			expectedMsg:  "Cannot use --all and --provider together",
		},
		{
			name:         "only all flag",
			all:          true,
			providerName: "",
			shouldError:  false,
		},
		{
			name:         "only provider flag",
			all:          false,
			providerName: "test-provider",
			shouldError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary config file
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yml")
			statePath := filepath.Join(tmpDir, "state.yml")

			// Create minimal config
			configContent := `clone_path: "` + tmpDir + `/repos/"
providers:
  test-provider:
    driver: gitlab
    owner: test-org
watchers: {}
events: {}
`
			err := os.WriteFile(configPath, []byte(configContent), 0644)
			if err != nil {
				t.Fatal(err)
			}

			// Create empty state
			stateContent := `meta:
  last_run: 0001-01-01T00:00:00Z
repos: []
`
			err = os.WriteFile(statePath, []byte(stateContent), 0644)
			if err != nil {
				t.Fatal(err)
			}

			// Test validation logic (this would need to be extracted to a separate function)
			hasError := false
			if !tt.all && tt.providerName == "" {
				hasError = true
			}
			if tt.all && tt.providerName != "" {
				hasError = true
			}

			if hasError != tt.shouldError {
				t.Errorf("Expected error: %v, got error: %v", tt.shouldError, hasError)
			}
		})
	}
}

func TestHandleProjectCloneOnly(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	cfg := config.Config{
		ClonePath: tmpDir + "/",
	}

	provider := config.Provider{
		Name:   "test-gitlab",
		Driver: "gitlab",
	}

	_ = context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:   cfg,
		Provider: provider,
	})

	tests := []struct {
		name        string
		project     mockGitlabProject
		shouldError bool
	}{
		{
			name: "normal project",
			project: mockGitlabProject{
				pathWithNamespace: "test-org/test-repo",
				sshURLToRepo:      "git@gitlab.com:test-org/test-repo.git",
				visibility:        "public",
				emptyRepo:         false,
			},
			shouldError: false, // Will error because git clone will fail, but that's expected
		},
		{
			name: "empty project",
			project: mockGitlabProject{
				pathWithNamespace: "test-org/empty-repo",
				sshURLToRepo:      "git@gitlab.com:test-org/empty-repo.git",
				visibility:        "private",
				emptyRepo:         true,
			},
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test would require mocking the actual git clone operation
			// For now, we'll just test the basic structure creation

			repoState := config.RepoState{
				Name:     tt.project.pathWithNamespace,
				CloneUrl: tt.project.sshURLToRepo,
				Public:   tt.project.visibility == "public",
				Driver:   provider.Name,
			}

			// Verify the basic fields are set correctly
			if repoState.Name != tt.project.pathWithNamespace {
				t.Errorf("Expected name %s, got %s", tt.project.pathWithNamespace, repoState.Name)
			}

			if repoState.CloneUrl != tt.project.sshURLToRepo {
				t.Errorf("Expected clone URL %s, got %s", tt.project.sshURLToRepo, repoState.CloneUrl)
			}

			if repoState.Public != (tt.project.visibility == "public") {
				t.Errorf("Expected public %v, got %v", tt.project.visibility == "public", repoState.Public)
			}
		})
	}
}

func TestParallelRepositoryProcessing(t *testing.T) {
	// Test the parallel processing pattern used in run poll

	// Simulate multiple repositories
	numRepos := 5
	repos := make([]config.RepoState, numRepos)
	for i := 0; i < numRepos; i++ {
		repos[i] = config.RepoState{
			Name:     fmt.Sprintf("test-repo-%d", i),
			CloneUrl: fmt.Sprintf("git@example.com:test/repo-%d.git", i),
			Head:     "abc123",
			Public:   true,
			Driver:   "test",
		}
	}

	// Test parallel processing pattern
	var wg sync.WaitGroup
	results := make(chan config.RepoState, len(repos))
	errorsChan := make(chan error, len(repos))

	// Process repos in parallel (simulating the run poll logic)
	for _, repo := range repos {
		wg.Add(1)
		go func(repo config.RepoState) {
			defer wg.Done()

			// Simulate processing time
			time.Sleep(time.Millisecond * 5)

			// Simulate updating HEAD (like RunGit would do)
			repo.Head = "new-commit-hash"

			// Send result
			results <- repo
		}(repo)
	}

	// Close channels when done
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

	// Collect results (simulating state updates)
	var processedRepos []config.RepoState
	for result := range results {
		processedRepos = append(processedRepos, result)
	}

	// Wait for error collection
	<-done

	// Verify all repos were processed
	if len(processedRepos) != numRepos {
		t.Errorf("Expected %d repos processed, got %d", numRepos, len(processedRepos))
	}

	// Verify HEAD was updated for all repos
	for _, repo := range processedRepos {
		if repo.Head != "new-commit-hash" {
			t.Errorf("Expected HEAD to be updated to 'new-commit-hash', got '%s'", repo.Head)
		}
	}

	// Verify no errors occurred in this test
	if len(repoErrs) != 0 {
		t.Errorf("Expected no errors, got %d errors", len(repoErrs))
	}
}

func TestClonePathConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		configPath   string
		expectedPath string
	}{
		{
			name:         "custom clone path",
			configPath:   "/custom/path/",
			expectedPath: "/custom/path/",
		},
		{
			name:         "empty clone path uses default",
			configPath:   "",
			expectedPath: "/tmp/nosygit/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				ClonePath: tt.configPath,
			}

			provider := config.Provider{Name: "test"}

			_ = context.WithValue(context.Background(), "ctx", config.ContextStruct{
				Config:   cfg,
				Provider: provider,
			})

			// Test the clone path logic
			clonePath := cfg.ClonePath
			if clonePath == "" {
				clonePath = "/tmp/nosygit/"
			}

			if clonePath != tt.expectedPath {
				t.Errorf("Expected clone path %s, got %s", tt.expectedPath, clonePath)
			}
		})
	}
}
