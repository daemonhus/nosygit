package logging

import (
	"encoding/json"
	"fmt"
	"nosygit/internal/config"
	"strings"
	"time"
)

type SinkEventV1 struct {
	Version string         `json:"version"`
	Kind    string         `json:"kind"`
	Event   string         `json:"event"`
	TS      string         `json:"ts"`
	Message string         `json:"message"`
	Context SinkContextV1  `json:"context"`
	Payload map[string]any `json:"payload"`
}

type SinkContextV1 struct {
	Watcher  string `json:"watcher,omitempty"`
	Provider string `json:"provider,omitempty"`
	Repo     string `json:"repo,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	Sink     string `json:"sink,omitempty"`
}

func BuildSinkEventV1(event Event, sink string) SinkEventV1 {
	now := time.Now().UTC()
	ts := inferEventTime(event.Meta, now).UTC().Format(time.RFC3339)
	name := canonicalEventName(event)
	message := strings.TrimSpace(event.Message)
	if message == "" {
		message = name
	}

	provider, repo := inferProviderAndRepo(event.Meta)
	ctx := SinkContextV1{
		Watcher: strings.TrimSpace(event.Name),
		Sink:    strings.TrimSpace(sink),
	}
	if provider != "" {
		ctx.Provider = provider
	}
	if repo != "" {
		ctx.Repo = repo
	}
	if eventKind(event.Type) != "watcher" {
		ctx.Watcher = ""
	}

	payload := payloadForMeta(event)

	return SinkEventV1{
		Version: "v1",
		Kind:    eventKind(event.Type),
		Event:   name,
		TS:      ts,
		Message: message,
		Context: ctx,
		Payload: payload,
	}
}

func MarshalSinkEventV1(event Event, sink string) ([]byte, error) {
	envelope := BuildSinkEventV1(event, sink)
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func eventKind(t EventType) string {
	switch t {
	case Repo, RepoVisibility, Branch:
		return "remote"
	case CommitNew, File, Regex, CommitMessage, CommitMetrics, MergeStats, Blob, ContributorFirstSeen, ChangeRequestOpened:
		return "watcher"
	case MetaInfo, MetaWarning, MetaError:
		return "meta"
	default:
		return "meta"
	}
}

func canonicalEventName(event Event) string {
	switch event.Type {
	case Repo:
		if m, ok := event.Meta.(RepoEventMeta); ok {
			switch strings.ToUpper(strings.TrimSpace(m.Operation)) {
			case "ADD":
				return "repo-added"
			case "DELETE":
				return "repo-removed"
			default:
				return "repo-updated"
			}
		}
		return "repo-updated"
	case RepoVisibility:
		return "repo-visibility-changed"
	case Branch:
		if m, ok := event.Meta.(BranchEventMeta); ok {
			switch strings.ToUpper(strings.TrimSpace(m.Operation)) {
			case "ADD":
				return "branch-added"
			case "DELETE":
				return "branch-removed"
			default:
				return "branch-updated"
			}
		}
		return "branch-updated"
	case MetaInfo:
		if strings.TrimSpace(event.Name) != "" {
			return strings.TrimSpace(event.Name)
		}
		return "runtime-info"
	case MetaWarning:
		if strings.TrimSpace(event.Name) != "" {
			return strings.TrimSpace(event.Name)
		}
		return "runtime-warning"
	case MetaError:
		if strings.TrimSpace(event.Name) != "" {
			return strings.TrimSpace(event.Name)
		}
		return "runtime-error"
	case ChangeRequestOpened:
		if m, ok := event.Meta.(ChangeRequestEventMeta); ok {
			action := strings.TrimSpace(strings.ToLower(m.Action))
			action = strings.ReplaceAll(action, "_", "-")
			if action != "" {
				return "change-request-" + action
			}
		}
		return "change-request-opened"
	default:
		if event.Type >= Repo && event.Type <= Branch {
			return event.Type.String()
		}
		n := strings.TrimSpace(event.Name)
		if n != "" {
			return n
		}
		return "runtime-message"
	}
}

func inferProviderAndRepo(meta interface{}) (string, string) {
	switch m := meta.(type) {
	case RepoEventMeta:
		return providerName(m.Provider), m.RepoName
	case RepoVisibilityEventMeta:
		return providerName(m.Provider), m.RepoName
	case FileEventMeta:
		return providerName(m.Provider), repoStateName(m.RepoState)
	case ContributorEventMeta:
		return providerName(m.Provider), m.RepoName
	case CommitEventMeta:
		return providerName(m.Provider), m.RepoName
	case ChangeRequestEventMeta:
		return providerName(m.Provider), m.RepoName
	case BranchEventMeta:
		return providerName(m.Provider), m.RepoName
	case MetaEventMeta:
		return providerName(m.Provider), repoStateName(m.RepoState)
	default:
		return "", ""
	}
}

func inferEventTime(meta interface{}, fallback time.Time) time.Time {
	switch m := meta.(type) {
	case FileEventMeta:
		if !m.Time.IsZero() {
			return m.Time
		}
	case ContributorEventMeta:
		if !m.Time.IsZero() {
			return m.Time
		}
	case CommitEventMeta:
		if !m.Time.IsZero() {
			return m.Time
		}
	case ChangeRequestEventMeta:
		if m.OpenedAt != nil && !m.OpenedAt.IsZero() {
			return *m.OpenedAt
		}
		if m.UpdatedAt != nil && !m.UpdatedAt.IsZero() {
			return *m.UpdatedAt
		}
	case MetaEventMeta:
		if !m.Time.IsZero() {
			return m.Time
		}
	}
	return fallback
}

func payloadForMeta(event Event) map[string]any {
	payload := map[string]any{"type": event.Type.String()}

	switch m := event.Meta.(type) {
	case RepoEventMeta:
		payload["operation"] = strings.ToUpper(strings.TrimSpace(m.Operation))
		payload["resource"] = "repo"
		if m.Visibility != "" {
			payload["visibility"] = m.Visibility
		}
	case RepoVisibilityEventMeta:
		payload["operation"] = "EDIT"
		payload["resource"] = "visibility"
		payload["from"] = m.From
		payload["to"] = m.To
	case BranchEventMeta:
		payload["operation"] = strings.ToUpper(strings.TrimSpace(m.Operation))
		payload["resource"] = "branch"
		payload["branch"] = m.BranchName
	case FileEventMeta:
		if m.Path != "" {
			payload["path"] = m.Path
		}
		if m.Operation != "" {
			payload["operation"] = strings.ToUpper(strings.TrimSpace(m.Operation))
		}
		payload["commit"] = map[string]any{
			"hash":       m.CommitHash,
			"first_line": m.FirstLine,
			"author":     m.Author,
		}
	case ContributorEventMeta:
		payload["commit"] = map[string]any{
			"hash":       m.CommitHash,
			"first_line": m.FirstLine,
			"committer":  m.CommitterName,
			"email":      m.CommitterMail,
		}
	case CommitEventMeta:
		payload["commit"] = map[string]any{
			"hash":            m.CommitHash,
			"first_line":      m.FirstLine,
			"author_name":     m.AuthorName,
			"author_email":    m.AuthorEmail,
			"committer_name":  m.CommitterName,
			"committer_email": m.CommitterMail,
		}
	case ChangeRequestEventMeta:
		payload["type"] = "change-request"
		payload["change_request"] = map[string]any{
			"external_id":   m.ExternalID,
			"number":        m.Number,
			"kind":          m.Kind,
			"action":        m.Action,
			"title":         m.Title,
			"description":   m.Description,
			"author_login":  m.AuthorLogin,
			"state":         m.State,
			"source_branch": m.SourceBranch,
			"target_branch": m.TargetBranch,
			"source_sha":    m.SourceSHA,
			"comments":      m.Comments,
			"opened_at":     m.OpenedAt,
			"updated_at":    m.UpdatedAt,
		}
	case MetaEventMeta:
		if m.Severity != "" {
			payload["severity"] = m.Severity
		}
		if m.Component != "" {
			payload["component"] = m.Component
		}
		if m.Code != "" {
			payload["code"] = m.Code
		}
		if len(m.Details) > 0 {
			payload["details"] = m.Details
		}
	}

	if textMetrics, ok := parseInlineMetrics(strings.TrimSpace(event.Message)); ok {
		payload["metrics"] = textMetrics
	}

	return payload
}

func parseInlineMetrics(message string) (map[string]int, bool) {
	if message == "" {
		return nil, false
	}
	labels := []string{"added", "changed", "deleted", "commits"}
	out := map[string]int{}
	for _, label := range labels {
		needle := label + "="
		idx := strings.Index(message, needle)
		if idx < 0 {
			continue
		}
		start := idx + len(needle)
		end := start
		for end < len(message) && message[end] >= '0' && message[end] <= '9' {
			end++
		}
		if end == start {
			continue
		}
		var val int
		if _, err := fmt.Sscanf(message[start:end], "%d", &val); err != nil {
			continue
		}
		out[label] = val
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func providerName(v interface{}) string {
	switch p := v.(type) {
	case config.Provider:
		return p.Name
	case *config.Provider:
		if p != nil {
			return p.Name
		}
	}
	return ""
}

func repoStateName(v interface{}) string {
	switch r := v.(type) {
	case config.RepoState:
		return r.Name
	case *config.RepoState:
		if r != nil {
			return r.Name
		}
	}
	return ""
}
