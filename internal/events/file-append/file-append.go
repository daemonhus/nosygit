package fileappend

import (
	"fmt"
	"nosygit/internal/config"
	"nosygit/internal/logging"
	"os"
	"sync"
)

var (
	fileSinkMu   sync.Mutex
	filePathLock = map[string]*sync.Mutex{}
)

func lockForPath(path string) *sync.Mutex {
	fileSinkMu.Lock()
	defer fileSinkMu.Unlock()
	if m, ok := filePathLock[path]; ok {
		return m
	}
	m := &sync.Mutex{}
	filePathLock[path] = m
	return m
}

func Trigger(ecfg config.Event, event logging.Event) error {
	path := ecfg.Config["path"]
	if path == "" {
		return fmt.Errorf("file-append sink missing config.path")
	}

	body, err := logging.MarshalSinkEventV1(event, ecfg.Driver)
	if err != nil {
		return err
	}

	pathMu := lockForPath(path)
	pathMu.Lock()
	defer pathMu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open sink file %q: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("write sink file %q: %w", path, err)
	}
	return nil
}
