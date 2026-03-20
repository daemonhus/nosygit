package cmd

import (
	"nosygit/internal/config"
	"testing"
	"time"
)

func TestIsProviderDue(t *testing.T) {
	now := time.Now()

	t.Run("no interval always due", func(t *testing.T) {
		due, err := isProviderDue(config.Provider{Name: "gh"}, now, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !due {
			t.Fatal("expected provider to be due")
		}
	})

	t.Run("first run due", func(t *testing.T) {
		due, err := isProviderDue(config.Provider{Name: "gh", PollEvery: "5m"}, time.Time{}, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !due {
			t.Fatal("expected provider to be due")
		}
	})

	t.Run("interval not elapsed", func(t *testing.T) {
		last := now.Add(-2 * time.Minute)
		due, err := isProviderDue(config.Provider{Name: "gh", PollEvery: "5m"}, last, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if due {
			t.Fatal("expected provider to be skipped")
		}
	})

	t.Run("interval elapsed", func(t *testing.T) {
		last := now.Add(-6 * time.Minute)
		due, err := isProviderDue(config.Provider{Name: "gh", PollEvery: "5m"}, last, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !due {
			t.Fatal("expected provider to be due")
		}
	})
}
