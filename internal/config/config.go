package config

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
	"os"
	"strconv"
	"strings"
	"time"
)

type Watcher struct {
	Name string
	Type string `yaml:"type"`

	// File + regex
	Regex       string   `yaml:"regex"`
	PathExclude string   `yaml:"path-exclude"`
	Glob        string   `yaml:"glob"`
	On          []string `yaml:"on"`

	// Metrics-style watchers
	MinAdded   int `yaml:"min_added"`
	MinChanged int `yaml:"min_changed"`
	MinDeleted int `yaml:"min_deleted"`

	// Blob watcher
	MinSize int `yaml:"min_size"`

	// Contributor watcher
	AuthorOrCommitter string   `yaml:"author_or_committer"`
	ExcludeBots       bool     `yaml:"exclude_bots"`
	BotPatterns       []string `yaml:"bot_patterns"`

	// Repo visibility watcher
	VisibilityFrom string `yaml:"visibility_from"`
	VisibilityTo   string `yaml:"visibility_to"`

	// Repo + repo visibility
	Provider     string `yaml:"provider"`
	Organisation string `yaml:"org"`
}

type Event struct {
	Name     string
	Watcher  string            `yaml:"watcher"`
	Watchers []string          `yaml:"watchers"`
	Driver   string            `yaml:"driver"`
	Config   map[string]string `yaml:"config"`
}

func (e Event) SelectedWatchers() []string {
	out := make([]string, 0, len(e.Watchers)+1)
	if strings.TrimSpace(e.Watcher) != "" {
		out = append(out, strings.TrimSpace(e.Watcher))
	}
	for _, w := range e.Watchers {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (e Event) MatchesWatcher(name string) bool {
	selected := e.SelectedWatchers()
	if len(selected) == 0 {
		return true
	}
	for _, w := range selected {
		if w == name {
			return true
		}
	}
	return false
}

type Provider struct {
	Name      string
	Driver    string `yaml:"driver"`
	Owner     string `yaml:"owner"`
	PollEvery string `yaml:"poll_every,omitempty"`
}

type WatcherMap struct {
	Name string
	Val  Watcher
}

type Config struct {
	Watchers  map[string]Watcher  `yaml:"watchers"`
	Events    map[string]Event    `yaml:"events"`
	Providers map[string]Provider `yaml:"providers"`
	ClonePath string              `yaml:"clone_path,omitempty"`
	Shallow   ShallowConfig       `yaml:"shallow,omitempty"`
}

type ShallowConfig struct {
	Enabled   bool `yaml:"enabled"`
	Depth     int  `yaml:"depth"`
	MaxDeepen int  `yaml:"max_deepen"`
}

func OpenConfig(path string) (config Config, err error) {
	configFile, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}

	configBytes, _ := io.ReadAll(configFile)
	err = yaml.Unmarshal(configBytes, &config)

	err = configFile.Close()
	if err != nil {
		return Config{}, err
	}

	// Map key -> .Name for each config entity
	for name, watcher := range config.Watchers {
		watcher.Name = name
		config.Watchers[name] = watcher
	}
	for name, event := range config.Events {
		event.Name = name
		config.Events[name] = event
	}
	for name, provider := range config.Providers {
		provider.Name = name
		if _, err := provider.PollInterval(); err != nil {
			return Config{}, err
		}
		config.Providers[name] = provider
	}

	return config, err
}

func (p Provider) PollInterval() (time.Duration, error) {
	raw := strings.TrimSpace(p.PollEvery)
	if raw == "" {
		return 0, nil
	}

	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds < 0 {
			return 0, fmt.Errorf("provider %q poll_every must be >= 0", p.Name)
		}
		return time.Duration(seconds) * time.Second, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("provider %q has invalid poll_every %q: %w", p.Name, p.PollEvery, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("provider %q poll_every must be >= 0", p.Name)
	}
	return d, nil
}

func (cfg Config) PrintWatchers() {
	fmt.Printf("👀 Watching %d rules: ", len(cfg.Watchers))
	watchers := ""
	for i := range cfg.Watchers {
		watchers += fmt.Sprintf("%s, ", i)
	}

	fmt.Printf("%s\n", strings.TrimRight(watchers, ", "))
}

func (cfg Config) GetWatcher(name string) (Watcher, bool) {
	for watcherName := range cfg.Watchers {
		if watcherName != name {
			continue
		}
		return cfg.Watchers[watcherName], true
	}

	return Watcher{}, false
}

func (w Watcher) OperationContains(op string) bool {
	for _, on := range w.On {
		if op == on {
			return true
		}
	}
	return false
}

func (cfg Config) GetProviders(driverType string) (providers []Provider) {
	for _, provider := range cfg.Providers {
		if provider.Driver == driverType {
			providers = append(providers, provider)
		}
	}

	return
}

func (cfg Config) GetProvider(driverName string) (provider Provider, ok bool) {
	for _, provider := range cfg.Providers {
		if provider.Name == driverName {
			return provider, true
		}
	}

	return Provider{}, false
}
