package events

import (
	"context"
	"fmt"
	"nosygit/internal/config"
	fileappend "nosygit/internal/events/file-append"
	"nosygit/internal/events/stdout"
	"nosygit/internal/logging"
	"sort"
	"strings"
	"time"
)

type EventSink interface {
	Trigger(logging.Event) error
}

// Trigger gets the config for drivers and delivers to the correct sink.
func Trigger(ctx context.Context, event logging.Event) {
	ctxValue, ok := ctx.Value("ctx").(config.ContextStruct)
	if !ok {
		return
	}
	cfg := ctxValue.Config

	if isMetaType(event.Type) {
		for _, sinkCfg := range allSinkConfigs(cfg) {
			if err := triggerSink(sinkCfg, event); err != nil {
				// Meta events are already sink-level diagnostics, do not recurse.
				_ = err
			}
		}
		return
	}

	for _, eventCfg := range cfg.Events {
		if !hasValidWatcherRefs(cfg, eventCfg) {
			continue
		}
		if !eventCfg.MatchesWatcher(event.Name) {
			continue
		}
		if err := triggerSink(eventCfg, event); err != nil {
			EmitMetaError(ctx, "sink", "SINK_DELIVERY_FAILED", "sink delivery failed", map[string]interface{}{
				"sink":    eventCfg.Driver,
				"watcher": eventCfg.Watcher,
				"error":   err.Error(),
			})
		}
	}
}

func hasValidWatcherRefs(cfg config.Config, eventCfg config.Event) bool {
	selected := eventCfg.SelectedWatchers()
	if len(selected) == 0 {
		return true
	}
	for _, w := range selected {
		if _, ok := cfg.GetWatcher(w); !ok {
			return false
		}
	}
	return true
}

func EmitMetaInfo(ctx context.Context, component string, code string, message string, details map[string]interface{}) {
	EmitMeta(ctx, logging.MetaInfo, "runtime-info", component, code, message, details)
}

func EmitMetaWarning(ctx context.Context, component string, code string, message string, details map[string]interface{}) {
	EmitMeta(ctx, logging.MetaWarning, "runtime-warning", component, code, message, details)
}

func EmitMetaError(ctx context.Context, component string, code string, message string, details map[string]interface{}) {
	EmitMeta(ctx, logging.MetaError, "runtime-error", component, code, message, details)
}

func EmitMeta(ctx context.Context, typ logging.EventType, eventName string, component string, code string, message string, details map[string]interface{}) {
	ctxValue, ok := ctx.Value("ctx").(config.ContextStruct)
	if !ok {
		return
	}

	meta := logging.MetaEventMeta{
		Severity:  severityForType(typ),
		Component: strings.TrimSpace(component),
		Code:      strings.TrimSpace(code),
		Details:   details,
		Time:      time.Now().UTC(),
		Provider:  ctxValue.Provider,
		RepoState: ctxValue.RepoState,
	}

	Trigger(ctx, logging.Event{
		Name:    eventName,
		Type:    typ,
		Meta:    meta,
		Message: strings.TrimSpace(message),
	})
}

func severityForType(typ logging.EventType) string {
	switch typ {
	case logging.MetaError:
		return "error"
	case logging.MetaWarning:
		return "warn"
	default:
		return "info"
	}
}

func isMetaType(typ logging.EventType) bool {
	return typ == logging.MetaInfo || typ == logging.MetaWarning || typ == logging.MetaError
}

func triggerSink(eventCfg config.Event, event logging.Event) error {
	switch eventCfg.Driver {
	case "stdout":
		return stdout.Trigger(eventCfg, event)
	case "file-append":
		return fileappend.Trigger(eventCfg, event)
	default:
		return fmt.Errorf("unknown sink driver: %s", eventCfg.Driver)
	}
}

func allSinkConfigs(cfg config.Config) []config.Event {
	out := make([]config.Event, 0, len(cfg.Events))
	seen := map[string]bool{}
	for _, e := range cfg.Events {
		key := sinkKey(e)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

func sinkKey(e config.Event) string {
	parts := []string{e.Driver}
	if len(e.Config) > 0 {
		keys := make([]string, 0, len(e.Config))
		for k := range e.Config {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, k+"="+e.Config[k])
		}
	}
	return strings.Join(parts, "|")
}
