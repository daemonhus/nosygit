package cmd

import (
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// maxConcurrentClones bounds simultaneous clones across the process. Clones open
// the most SSH connections at once, so they get a lower limit than fetches.
const maxConcurrentClones = 4

var cloneSem = make(chan struct{}, maxConcurrentClones)

const authRetryAttempts = 3

// authRetryBackoff is the base delay between retries; it doubles each attempt.
var authRetryBackoff = time.Second

var sshAuthState struct {
	mu     sync.Mutex
	done   bool
	auth   transport.AuthMethod
	err    error
	closer io.Closer
}

// resolveSSHAuth returns the credentials for git@ remotes, building them once
// per run so thousands of repos do not each dial the agent and read key files.
func resolveSSHAuth() (transport.AuthMethod, error) {
	sshAuthState.mu.Lock()
	defer sshAuthState.mu.Unlock()
	if !sshAuthState.done {
		sshAuthState.auth, sshAuthState.closer, sshAuthState.err = buildSSHAuth()
		sshAuthState.done = true
	}
	return sshAuthState.auth, sshAuthState.err
}

// resetSSHAuth discards the cached credentials and closes the agent connection.
// Call it at the start of each run so a restarted agent or new key is noticed.
func resetSSHAuth() {
	sshAuthState.mu.Lock()
	defer sshAuthState.mu.Unlock()
	closeQuietly(sshAuthState.closer)
	sshAuthState.done = false
	sshAuthState.auth, sshAuthState.err, sshAuthState.closer = nil, nil, nil
}

// isTransientSSHErr reports connection-level failures that busy servers cause
// when many logins arrive at once, as opposed to real authorisation errors.
func isTransientSSHErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{"unable to authenticate", "handshake failed", "connection reset", "broken pipe"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// withAuthRetry retries fn with exponential backoff and jitter while it fails
// with a transient SSH error.
func withAuthRetry(fn func() error) error {
	var err error
	for attempt := 0; attempt < authRetryAttempts; attempt++ {
		err = fn()
		if !isTransientSSHErr(err) {
			return err
		}
		if attempt < authRetryAttempts-1 && authRetryBackoff > 0 {
			delay := authRetryBackoff << attempt
			time.Sleep(delay + time.Duration(rand.Int63n(int64(delay)/2+1)))
		}
	}
	return err
}
