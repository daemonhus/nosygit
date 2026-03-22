package events

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"nosygit/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return buf.String()
}

func parseOneJSONLine(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	s := bufio.NewScanner(strings.NewReader(out))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid json line %q: %v", line, err)
		}
		return record
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan output: %v", err)
	}
	t.Fatalf("expected at least one json line")
	return nil
}

func TestEmitMetaError_RoutesToSinksAsJSONContract(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.log")

	cfg := config.Config{
		Events: map[string]config.Event{
			"stdout-any": {
				Watcher: "any",
				Driver:  "stdout",
			},
			"file-any": {
				Watcher: "any",
				Driver:  "file-append",
				Config: map[string]string{
					"path": logPath,
				},
			},
		},
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{
		Config:    cfg,
		Provider:  config.Provider{Name: "gh"},
		RepoState: config.RepoState{Name: "acme/api"},
	})

	stdout := captureStdout(t, func() {
		EmitMetaError(ctx, "runner", "E2E_META", "meta test", map[string]interface{}{"x": "y"})
	})
	stdoutRecord := parseOneJSONLine(t, stdout)
	if stdoutRecord["version"] != "v1" || stdoutRecord["kind"] != "meta" || stdoutRecord["event"] != "runtime-error" {
		t.Fatalf("unexpected stdout meta envelope: %#v", stdoutRecord)
	}

	fileBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read file sink output: %v", err)
	}
	fileRecord := parseOneJSONLine(t, string(fileBytes))
	if fileRecord["version"] != "v1" || fileRecord["kind"] != "meta" || fileRecord["event"] != "runtime-error" {
		t.Fatalf("unexpected file meta envelope: %#v", fileRecord)
	}
}
