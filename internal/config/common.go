package config

import "nosygit/internal/storage"

type ContextStruct struct {
	Config    Config
	State     State
	RepoState RepoState
	Provider  Provider
	Store     *storage.Store
	Verbose   bool
}
