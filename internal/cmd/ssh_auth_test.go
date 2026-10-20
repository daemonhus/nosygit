package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"

	GitSSH "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// serveEmptyAgent starts an ssh agent with no keys and returns its socket path.
func serveEmptyAgent(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	keyring := agent.NewKeyring()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(keyring, c)
		}
	}()
	return sock
}

func writeTestKey(t *testing.T, path string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSSHAuthEmptyAgentDoesNotHideKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	writeTestKey(t, keyPath)
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", serveEmptyAgent(t))
	t.Setenv("SSH_KEY_PATH", keyPath)
	t.Setenv("SSH_KNOWN_HOSTS", knownHosts)
	t.Setenv("HOME", t.TempDir())

	auth, err := resolveSSHAuth()
	if err != nil {
		t.Fatalf("expected the key file to be used with an empty agent: %v", err)
	}
	callback, ok := auth.(*GitSSH.PublicKeysCallback)
	if !ok {
		t.Fatalf("unexpected auth type %T", auth)
	}
	signers, err := callback.Callback()
	if err != nil || len(signers) != 1 {
		t.Fatalf("expected the key file signer to be offered, got %d signers (err %v)", len(signers), err)
	}
}

func TestResolveSSHAuthErrorsWithoutCredentials(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("SSH_KEY_PATH", "")
	t.Setenv("HOME", t.TempDir())

	if _, err := resolveSSHAuth(); err == nil {
		t.Fatal("expected an error when no credentials exist")
	}
}
