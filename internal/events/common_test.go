package events

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"nosygit/internal/config"
	"nosygit/internal/logging"
	"os"
	"strings"
	"testing"
)

func captureStdoutLocal(t *testing.T, fn func()) string {
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

func countJSONLines(t *testing.T, out string) int {
	t.Helper()
	count := 0
	s := bufio.NewScanner(strings.NewReader(out))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		count++
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan output: %v", err)
	}
	return count
}

func TestTrigger_MultipleWatchers(t *testing.T) {
	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"w1": {Type: "regex"},
			"w2": {Type: "regex"},
		},
		Events: map[string]config.Event{
			"multi": {
				Watchers: []string{"w1", "w2"},
				Driver:   "stdout",
			},
		},
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: cfg})

	out := captureStdoutLocal(t, func() {
		Trigger(ctx, logging.Event{Name: "w2", Type: logging.Regex, Message: "hit"})
	})
	if got := countJSONLines(t, out); got != 1 {
		t.Fatalf("expected 1 JSON event line, got %d; out=%q", got, out)
	}
}

func TestTrigger_NoWatcherMeansAll(t *testing.T) {
	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"w1": {Type: "regex"},
		},
		Events: map[string]config.Event{
			"all": {
				Driver: "stdout",
			},
		},
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: cfg})

	out := captureStdoutLocal(t, func() {
		Trigger(ctx, logging.Event{Name: "w1", Type: logging.Regex, Message: "hit"})
	})
	if got := countJSONLines(t, out); got != 1 {
		t.Fatalf("expected 1 JSON event line, got %d; out=%q", got, out)
	}
}

func TestTrigger_InvalidWatcherReferenceSkipsSink(t *testing.T) {
	cfg := config.Config{
		Watchers: map[string]config.Watcher{
			"w1": {Type: "regex"},
		},
		Events: map[string]config.Event{
			"bad": {
				Watchers: []string{"missing"},
				Driver:   "stdout",
			},
		},
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{Config: cfg})

	out := captureStdoutLocal(t, func() {
		Trigger(ctx, logging.Event{Name: "w1", Type: logging.Regex, Message: "hit"})
	})
	if got := countJSONLines(t, out); got != 0 {
		t.Fatalf("expected 0 JSON event lines, got %d; out=%q", got, out)
	}
}
