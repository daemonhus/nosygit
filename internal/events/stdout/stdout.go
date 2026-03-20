package stdout

import (
	"nosygit/internal/config"
	"nosygit/internal/logging"
	"os"
	"sync"
)

var stdoutMu sync.Mutex

func Trigger(ecfg config.Event, event logging.Event) error {
	encoded, err := logging.MarshalSinkEventV1(event, ecfg.Driver)
	if err != nil {
		return err
	}
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	if _, err := os.Stdout.Write(encoded); err != nil {
		return err
	}
	return nil
}
