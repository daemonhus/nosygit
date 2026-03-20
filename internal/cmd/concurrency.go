package cmd

import (
	"nosygit/internal/config"
	"sync"
)

func collectRepoStateAndErrors(results <-chan config.RepoState, errorsChan <-chan error) ([]config.RepoState, []error) {
	repoStates := make([]config.RepoState, 0)
	errs := make([]error, 0)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for result := range results {
			repoStates = append(repoStates, result)
		}
	}()

	go func() {
		defer wg.Done()
		for err := range errorsChan {
			errs = append(errs, err)
		}
	}()

	wg.Wait()
	return repoStates, errs
}
