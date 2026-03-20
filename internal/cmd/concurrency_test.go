package cmd

import (
	"errors"
	"nosygit/internal/config"
	"sync"
	"testing"
	"time"
)

func TestCollectRepoStateAndErrors_NoDeadlock(t *testing.T) {
	results := make(chan config.RepoState)
	errorsChan := make(chan error)
	done := make(chan struct{})

	var workerWg sync.WaitGroup
	workerCount := 20
	workerWg.Add(workerCount)

	for i := 0; i < workerCount; i++ {
		go func(i int) {
			defer workerWg.Done()
			errorsChan <- errors.New("simulated error")
			results <- config.RepoState{Name: "repo", CloneUrl: "git@example.com/repo.git"}
		}(i)
	}

	go func() {
		workerWg.Wait()
		close(results)
		close(errorsChan)
	}()

	go func() {
		repoStates, errs := collectRepoStateAndErrors(results, errorsChan)
		if len(repoStates) != workerCount {
			t.Errorf("expected %d repo states, got %d", workerCount, len(repoStates))
		}
		if len(errs) != workerCount {
			t.Errorf("expected %d errors, got %d", workerCount, len(errs))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("collector timed out, possible deadlock")
	}
}
