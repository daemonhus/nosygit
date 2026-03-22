package config

import (
	"testing"
	"time"
)

func TestEventWatcherSelection(t *testing.T) {
	t.Run("single watcher field", func(t *testing.T) {
		e := Event{Watcher: "w1"}
		if !e.MatchesWatcher("w1") {
			t.Fatal("expected watcher match")
		}
		if e.MatchesWatcher("w2") {
			t.Fatal("did not expect non-selected watcher match")
		}
	})

	t.Run("multiple watcher fields", func(t *testing.T) {
		e := Event{Watcher: "w1", Watchers: []string{"w2", "w3"}}
		if !e.MatchesWatcher("w1") || !e.MatchesWatcher("w2") || !e.MatchesWatcher("w3") {
			t.Fatal("expected all selected watchers to match")
		}
		if e.MatchesWatcher("w4") {
			t.Fatal("did not expect unselected watcher to match")
		}
	})

	t.Run("no watcher means all", func(t *testing.T) {
		e := Event{}
		if !e.MatchesWatcher("anything") {
			t.Fatal("expected empty watcher selector to match all")
		}
	})
}

func TestProviderPollInterval(t *testing.T) {
	t.Run("empty means no interval", func(t *testing.T) {
		p := Provider{Name: "gh"}
		d, err := p.PollInterval()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != 0 {
			t.Fatalf("expected 0 duration, got %s", d)
		}
	})

	t.Run("seconds shorthand", func(t *testing.T) {
		p := Provider{Name: "gh", PollEvery: "30"}
		d, err := p.PollInterval()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != 30*time.Second {
			t.Fatalf("expected 30s, got %s", d)
		}
	})

	t.Run("duration format", func(t *testing.T) {
		p := Provider{Name: "gl", PollEvery: "2m"}
		d, err := p.PollInterval()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != 2*time.Minute {
			t.Fatalf("expected 2m, got %s", d)
		}
	})

	t.Run("invalid value", func(t *testing.T) {
		p := Provider{Name: "gl", PollEvery: "abc"}
		if _, err := p.PollInterval(); err == nil {
			t.Fatal("expected error")
		}
	})
}
