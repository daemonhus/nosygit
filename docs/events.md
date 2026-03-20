# Events

This document defines the outgoing message contract for event sinks (`stdout`, `file-append`, and future sinks).

## Contract goals

- Every emitted message must be machine-parseable.
- Every emitted message must include enough context to route, alert, and debug.
- Event shape must be consistent across watcher and non-watcher flows.

## Envelope (v1)

All outgoing sink messages should be a single JSON object per line (JSONL / NDJSON), UTF-8 encoded, newline-delimited.

Required top-level fields:

| Field | Type | Description |
|---|---|---|
| `version` | string | Contract version. Initial value: `"v1"`. |
| `kind` | string | Event group. One of: `watcher`, `remote`, `meta`. |
| `event` | string | Concrete event name (for example `commit-message`, `repo-added`, `runtime-error`). |
| `ts` | string | Event timestamp in RFC3339 format, UTC recommended. |
| `message` | string | Human-readable summary line. |
| `context` | object | Common structured context for routing and correlation. |
| `payload` | object | Event-specific structured data. |

`context` fields:

| Field | Type | Required | Description |
|---|---|---|---|
| `watcher` | string | `watcher` kind only | Watcher config name that triggered this event. |
| `provider` | string | optional | Provider key from config (for example `gh`, `gitlab-main`). |
| `repo` | string | optional | Repository name (`org/repo`). |
| `run_id` | string | optional | Correlation id for one daemon run/poll cycle. |
| `sink` | string | optional | Sink driver receiving this event. |

## Event kinds

### 1) `watcher`

Events triggered by configured watchers.

Common watcher events (current codebase):

- `file`
- `regex`
- `commit-message`
- `commit-metrics`
- `merge-stats`
- `blob`
- `contributor-first-seen`
- `commit-new`
- `change-request-created`
- `change-request-closed`
- `change-request-merged`
- `change-request-new-commits`
- `change-request-changed`

Watcher payload shape:

| Field | Type | Description |
|---|---|---|
| `type` | string | Same as `event` (normalized watcher signal type). |
| `operation` | string | Optional operation (for example `ADD`, `EDIT`, `REMOVE`). |
| `commit` | object | Optional commit info (`hash`, `first_line`, author/committer fields). |
| `path` | string | Optional path for file/regex/blob style events. |
| `metrics` | object | Optional numeric metrics (`added`, `changed`, `deleted`, etc.). |

Example:

```json
{
  "version": "v1",
  "kind": "watcher",
  "event": "commit-message",
  "ts": "2026-02-15T18:21:13Z",
  "message": "Commit message matched in 9f4e2a1c: feat: add token lint",
  "context": {
    "watcher": "suspicious-commit-msg",
    "provider": "gh",
    "repo": "acme/api"
  },
  "payload": {
    "type": "commit-message",
    "commit": {
      "hash": "9f4e2a1c",
      "first_line": "feat: add token lint"
    }
  }
}
```

### 2) `remote`

Events from provider/repository state transitions not directly tied to a content watcher trigger.

Expected remote events:

- `repo-added`
- `repo-removed`
- `repo-visibility-changed`
- `branch-added`
- `branch-removed`

Remote payload shape:

| Field | Type | Description |
|---|---|---|
| `operation` | string | State transition operation (`ADD`, `DELETE`, `EDIT`). |
| `resource` | string | Resource type (`repo`, `branch`, `visibility`). |
| `from` | string | Previous value (for transitions such as visibility). |
| `to` | string | New value (for transitions such as visibility). |
| `branch` | string | Branch name for branch lifecycle events. |

Example:

```json
{
  "version": "v1",
  "kind": "remote",
  "event": "branch-added",
  "ts": "2026-02-15T18:21:14Z",
  "message": "Branch add in acme/api: feature/audit",
  "context": {
    "provider": "gh",
    "repo": "acme/api"
  },
  "payload": {
    "operation": "ADD",
    "resource": "branch",
    "branch": "feature/audit"
  }
}
```

### 3) `meta`

Runtime/operational events from the daemon itself.

Common meta events:

- `run-started`
- `run-finished`
- `runtime-info`
- `runtime-warning`
- `runtime-error`

Common meta `code` values currently emitted include:

- `RUN_STARTED`
- `RUN_FINISHED`
- `PROVIDER_FETCH_START`
- `PROVIDER_FETCH_DONE`
- `PROVIDER_SKIPPED`
- `SINK_DELIVERY_FAILED`

Meta payload shape:

| Field | Type | Description |
|---|---|---|
| `severity` | string | `info`, `warn`, or `error`. |
| `component` | string | Subsystem (`poller`, `watcher-engine`, `sink`, `config`, etc.). |
| `code` | string | Stable short error/status code. |
| `details` | object | Free-form structured diagnostics. |

Example:

```json
{
  "version": "v1",
  "kind": "meta",
  "event": "runtime-error",
  "ts": "2026-02-15T18:21:16Z",
  "message": "Failed to retrieve diff for 3ab98f2",
  "context": {
    "provider": "gh",
    "repo": "acme/api",
    "run_id": "run-20260215-1821"
  },
  "payload": {
    "severity": "error",
    "component": "watcher-engine",
    "code": "DIFF_PATCH_FAILURE",
    "details": {
      "commit": "3ab98f2"
    }
  }
}
```

## Naming rules

- Use lowercase kebab-case for `event` and stable enum-like values.
- Keep `message` concise and human-readable; never use it as the only source of data.
- Put structured fields in `payload`, not inside `message`.

## Backward compatibility

- Adding optional fields is non-breaking.
- Renaming/removing fields requires a new `version`.
- Sinks should preserve unknown fields.

## Implementation status

Current implementation emits this contract as JSONL across sink types (`stdout`, `file-append`):

1. One JSON object per event line.
2. Shared centralized envelope builder and serializer.
3. Watcher + remote events route to configured watcher sinks.
4. Meta events are broadcast to all configured unique sinks.
