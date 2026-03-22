package cmd

import "nosygit/internal/storage"

func openStore(path string) (*storage.Store, error) {
	return storage.Open(path)
}
