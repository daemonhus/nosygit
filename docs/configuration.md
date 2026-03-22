# Configuration

<p align="center">
  <a href="#schema">Schema</a> •
  <a href="#providers">Providers</a> •
  <a href="#watcher-reference">Watchers</a> •
  <a href="#event">Events</a>
</p>

## Schema

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `watchers` | object | yes | Map of watcher names to watcher configs. Defines signals to observe. |
| `events` | object | yes | Map of event names to event configs. Defines outcomes when watchers match. |
| `providers` | object | yes | Map of provider names to provider configs. Defines where to fetch repositories. |
| `clone_path` | string | no | Directory where repositories are cloned. Default: `/tmp/nosygit/`. |
| `shallow` | object | no | Optional shallow fetch settings. See below. |
| `webhook` | object | no | Optional webhook server settings. See [Webhook Server](#webhook-server). |

Optional shallow mode lets `nosygit` fetch a limited commit window per poll.

| Property | Type | Description |
|----------|------|-------------|
| `enabled` | bool | Toggles shallow fetch mode. |
| `depth` | int | Initial depth used on each fetch. |
| `max_deepen` | int | Retry count when previous stored HEAD is not reachable in shallow history. |

```yaml
shallow:
  enabled: true
  depth: 50
  max_deepen: 3
```


## Providers

Providers define sources for repositories. The provider key (e.g. `github-main`) is referenced by watchers via the `provider` property.

**Driver options:** `github`, `gitlab`, `git`.

### GitHub provider

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `driver` | string | yes | Must be `github`. |
| `owner` | string | yes | GitHub organisation or user login whose repositories to fetch (e.g. `acme` or `daemonhus`). |
| `poll_every` | string/int | no | Minimum interval between polls for this provider. Examples: `30` (seconds), `5m`, `1h`. Omit to poll on every `run poll` run. Set to `never` to disable polling (webhook-only mode). |
| `webhook_secret` | string | no | Secret used to validate the HMAC-SHA256 signature on incoming GitHub webhook deliveries (`X-Hub-Signature-256`). Required when using `nosygit run listen`. If omitted, signature validation is skipped (not recommended for production). |

### GitLab provider

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `driver` | string | yes | Must be `gitlab`. |
| `owner` | string | yes | Prefix to filter repositories by `PathWithNamespace` (e.g. `acme` or `acme/` to match `acme/*` repos). |
| `poll_every` | string/int | no | Minimum interval between polls for this provider. Examples: `30` (seconds), `5m`, `1h`. Omit to poll on every `run poll` run. Set to `never` to disable polling (webhook-only mode). |
| `webhook_secret` | string | no | Shared token validated against the `X-Gitlab-Token` header on incoming GitLab webhook deliveries. Required when using `nosygit run listen`. If omitted, token validation is skipped (not recommended for production). |

### Git (plain remote) provider

Tracks a single repository at an explicit remote URL. nosygit manages the clone lifecycle — no API access or repo discovery. Each `git` provider entry maps to one repository.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `driver` | string | yes | Must be `git`. |
| `url` | string | yes | Remote URL to clone. Supports HTTPS (`https://host/org/repo.git`) and SSH (`git@host:org/repo.git`) forms. |
| `poll_every` | string/int | no | Minimum interval between polls. Examples: `30` (seconds), `5m`, `1h`. Omit to poll on every `run poll` run. |

The repo name stored in state is derived from the URL path (e.g. `https://git.example.com/org/repo.git` → `org/repo`). Repositories are cloned to `clone_path/<derived-name>` using the global `clone_path` setting.

```yaml
providers:
  internal-tools:
    driver: git
    url: https://git.example.com/org/tools.git
    poll_every: 5m

  infra-repo:
    driver: git
    url: git@git.example.com:org/infra.git
    poll_every: 1h
```

## Webhook Server

`nosygit run listen` starts an HTTP server that accepts incoming webhook deliveries from GitHub and GitLab. Configure the server bind address under the top-level `webhook` key:

| Property | Type | Description |
|----------|------|-------------|
| `port` | int | Port to listen on. Default: `8080`. Can be overridden with `--port`. |
| `host` | string | Host/address to bind to. Default: `""` (all interfaces). Can be overridden with `--host`. |

```yaml
webhook:
  port: 8080
  host: ""
```

Each provider receives its own endpoint at `POST /webhook/<provider-name>`, where `<provider-name>` is the key used in the `providers` map. The provider's `driver` field determines which validation and parsing logic is applied.

**Routing example:**

```yaml
providers:
  github-main:           # → POST /webhook/github-main
    driver: github
    owner: acme
    poll_every: never    # disable polling; webhooks only
    webhook_secret: "gh-secret"

  gitlab-internal:       # → POST /webhook/gitlab-internal
    driver: gitlab
    owner: mygroup
    poll_every: 1h       # also poll hourly as a fallback
    webhook_secret: "gl-token"
```

**`poll_every: never`** skips the provider entirely during `nosygit run poll` and `nosygit fetch` runs. Use this for webhook-only providers where polling is unnecessary or undesired. Providers without this setting can run both polling and webhooks in parallel — all state updates are idempotent.

**Supported event types:**

| Provider | Event | Action |
|----------|-------|--------|
| GitHub | `push` | Full git fetch + watcher evaluation (file, regex, commit-message, contributor, branch watchers) |
| GitHub | `pull_request` | Updates change request state; triggers `change-request` watchers |
| GitHub | `create` / `delete` (branch) | Triggers `branch` watchers |
| GitHub | `repository` (created/deleted/publicized/privatized) | Triggers `repo` and `repo-visibility` watchers |
| GitLab | `Push Hook` | Full git fetch + watcher evaluation |
| GitLab | `Merge Request Hook` | Updates change request state; triggers `change-request` watchers |
| GitLab | `System Hook` (project_create/project_destroy) | Triggers `repo` watchers |

## Watcher Reference

Every watcher lives under `watchers.<name>` and is referenced by events via `events.<event>.watcher`.

### Common shape

```yaml
watchers:
  my-watcher-name:
    type: <type>
    # ...type-specific keys...
```

### `file` watcher

Detects file touch operations inside a commit diff.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `file`. |
| `glob` | string | yes | Glob pattern matched against file path (e.g. `Dockerfile`, `.github/workflows/**`, `**/*.tf`). |
| `on` | list | yes | File operations to match. Values: `CREATE`, `EDIT`, `REMOVE`. |

```yaml
watchers:
  codeowners-change:
    type: file
    glob: ".github/CODEOWNERS"
    on:
      - EDIT
```

```text
c0 (already processed head)
  |
  v
c1  "docs: update readme"
    - edits README.md
  |
  v
c2  "chore: update code owners"
    - edits .github/CODEOWNERS  <-- matches glob + EDIT, triggers
```

### `regex` watcher

Detects matching text in changed patch chunks.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `regex`. |
| `regex` | string | yes | Regular expression to search in changed chunk content. |
| `path-exclude` | string | yes | Regex applied to file path before content matching; paths matching this are skipped. Use `^$` to exclude nothing. |
| `on` | list | yes | Content operations to match. Values: `ADD`, `UPDATE`, `REMOVE`. |

```yaml
watchers:
  new-route-added:
    type: regex
    regex: "@route"
    path-exclude: "^$"
    on:
      - ADD
```

```text
c0 (already processed head)
  |
  v
c1  "refactor: move files"
    - no matching regex content
  |
  v
c2  "feat: add aws integration"
    - adds line: const key = "AKIA1234567890ABCDEF" in src/aws.go
      path src/aws.go does not match path-exclude
      chunk operation is ADD
      regex matches  <-- triggers
```

If you want no path exclusions, set `path-exclude: "^$"` (matches nothing).

### `repo` watcher

Detects repository lifecycle changes (add/delete) from provider polling.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `repo`. |
| `provider` | string | yes | Provider key from `providers` (e.g. `github-main`). |
| `org` | string | no | Prefix filter for repo names (e.g. `acme/`). Empty string matches all. |
| `on` | list | yes | Repository operations to match. Values: `ADD`, `DELETE`. |

```yaml
watchers:
  new-github-repo:
    type: repo
    provider: gh
    org: ""
    on:
      - ADD
      - DELETE
```

```text
poll #10 state: acme/api, acme/web
poll #11 provider list: acme/api, acme/web, acme/new-service
                                            ^^^^^^^^^^^^^^^^ ADD detected, triggers
```

### `repo-visibility` watcher

Detects visibility transitions (public ↔ private) for existing repositories.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `repo-visibility`. |
| `provider` | string | yes | Provider key from `providers`. |
| `org` | string | no | Prefix filter for repo names. Empty string matches all. |
| `on` | list | no | If present, may include `EDIT`. Omitted means all visibility changes are evaluated. |
| `visibility_from` | string | no | If set, only matches transitions from this visibility (case-insensitive). |
| `visibility_to` | string | no | If set, only matches transitions to this visibility (case-insensitive). |

Example:

```yaml
watchers:
  repo-changed-visibility:
    type: repo-visibility
    provider: gh
    org: ""
    visibility_from: private
    visibility_to: public
```

```yaml
watchers:
  repo-changed-visibility:
    type: repo-visibility
    provider: gh
    org: ""
```

```text
poll #20 state: acme/payments visibility=Private
poll #21 provider: acme/payments visibility=Public
                                 ^^^^^^^^^^^^^^^^ EDIT detected, triggers
```

### `branch` watcher

Detects remote branch create/delete during fetch.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `branch`. |
| `provider` | string | yes | Provider key from `providers`. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |
| `on` | list | yes | Branch operations to match. Values: `ADD`, `DELETE`. |

```yaml
watchers:
  branch-lifecycle:
    type: branch
    provider: gh
    org: ""
    on:
      - ADD
      - DELETE
```

### `commit-new` watcher

Triggers on every new commit seen during repository processing.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `commit-new`. |
| `provider` | string | no | If set, only triggers for this provider. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |

```yaml
watchers:
  every-commit:
    type: commit-new
    provider: gh
    org: acme/
```

### `contributor-first-seen` watcher

Detects first observed contributor identities per repository. Does not use `on`.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `contributor-first-seen`. |
| `provider` | string | no | If set, only triggers for commits from this provider. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |
| `author_or_committer` | string | no | Which identity to evaluate: `author`, `committer`, or `both`. Default: `committer`. |
| `exclude_bots` | bool | no | If `true`, matching bot identities are ignored. Default: `false`. |
| `bot_patterns` | list | no | Regex list used when `exclude_bots=true`. If omitted, built-in defaults are used. |

Example:

```yaml
watchers:
  first-committer-in-org:
    type: contributor-first-seen
    provider: gh
    org: acme/
    author_or_committer: both
    exclude_bots: true
    bot_patterns:
      - "(?i)\\[bot\\]"
      - "(?i)dependabot"
```

### `commit-message` watcher

Detects commit messages that match a regex.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `commit-message`. |
| `regex` | string | yes | Regex matched against full commit message text. |
| `provider` | string | no | If set, only triggers for this provider. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |

Example:

```yaml
watchers:
  jira-commit:
    type: commit-message
    regex: "JIRA-[0-9]+"
    provider: gh
    org: acme/
```

### `commit-metrics` watcher

Emits per-commit line metrics (`added`, `changed`, `deleted`) derived from diff chunks.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `commit-metrics`. |
| `min_added` | int | no | Minimum added lines required to trigger. Default: `0`. |
| `min_changed` | int | no | Minimum changed lines required to trigger. Default: `0`. |
| `min_deleted` | int | no | Minimum deleted lines required to trigger. Default: `0`. |
| `provider` | string | no | If set, only triggers for this provider. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |

Example:

```yaml
watchers:
  every-commit-metrics:
    type: commit-metrics
    min_added: 50
    min_changed: 10
    min_deleted: 10
    provider: gh
    org: acme/
```

### `merge-stats` watcher

Emits metrics at merge commits: number of commits since previous merge, plus line metrics (`added`, `changed`, `deleted`) across those commits.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `merge-stats`. |
| `min_added` | int | no | Minimum added lines required to trigger. Default: `0`. |
| `min_changed` | int | no | Minimum changed lines required to trigger. Default: `0`. |
| `min_deleted` | int | no | Minimum deleted lines required to trigger. Default: `0`. |
| `provider` | string | no | If set, only triggers for this provider. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |

Example:

```yaml
watchers:
  merge-window-stats:
    type: merge-stats
    min_added: 200
    min_changed: 50
    min_deleted: 20
    provider: gh
    org: acme/
```

### `blob` watcher

Emits per-commit file-level operation counts (`added`, `changed`, `deleted` files).

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `blob`. |
| `min_size` | int | no | Minimum changed bytes per file patch to count toward added/changed/deleted blob totals. Default: `10240` (~10KB). |
| `provider` | string | no | If set, only triggers for this provider. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |

Example:

```yaml
watchers:
  blob-ops:
    type: blob
    min_size: 10240
    provider: gh
    org: acme/
```

### `change-request` watcher

Emits pull request / merge request lifecycle events from provider API polling.

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `type` | string | yes | Must be `change-request`. |
| `provider` | string | no | If set, only triggers for this provider. |
| `org` | string | no | Prefix filter for repo names. Empty matches all. |
| `on` | list | no | Action filters. Values: `created`, `closed`, `merged`, `new-commits`, `changed`. Omit to match all actions. |

Example:

```yaml
watchers:
  change-request-lifecycle:
    type: change-request
    provider: gh
    org: acme/
    on:
      - created
      - merged
      - new-commits
      - changed
```

Backward compatibility:
- `type: change-request-opened` is still supported and behaves like `type: change-request` with `on: [created]`.

Identity key rules: primary key is normalized committer email (`email:<lowercased-email>`); fallback when email is empty is normalized name (`name:<lowercased-name>`).

```text
c0 (already processed head)
  |
  v
c1  committer: Alice <alice@acme.com>
    - already known in repo contributor state
  |
  v
c2  committer: Bob <bob@acme.com>
    - first time this identity is seen in this repo  <-- triggers
  |
  v
c3  committer: Bob <bob@acme.com>
    - already known now, no trigger
```

## Event

Events connect watcher matches to output sinks. Each event maps one watcher to one driver.
All sink output is JSONL using the contract in [`./events.md`](./events.md).

| Property | Type | Required | Description |
|----------|------|----------|-------------|
| `watcher` | string | no | Single watcher name from `watchers`. |
| `watchers` | list | no | Multiple watcher names from `watchers`. |
| `driver` | string | yes | Output driver: `stdout` or `file-append`. |
| `config` | object | no | Driver-specific key-value options. |

Watcher matching rules:

- If `watcher` is set, it is included in the match set.
- If `watchers` is set, all listed watcher names are included in the match set.
- If both are set, they are combined.
- If neither `watcher` nor `watchers` is set, the event matches all watcher-triggered events.

### `stdout` driver

Writes JSON event lines to standard output. No `config` keys.

### `file-append` driver

Appends JSON event lines to a local file. Supports:

| Config key | Type | Description |
|------------|------|-------------|
| `path` | string | File path to append to. |

```yaml
events:
  print-findings:
    watchers:
      - new-route-added
      - security-mention
    driver: stdout

  save-findings:
    watcher: new-route-added
    driver: file-append
    config:
      path: /var/log/nosygit-events.log

  print-all-watchers:
    driver: stdout
```

Note: `meta` events are runtime/operational outputs and are broadcast to all configured sink destinations (deduped by sink config), not only watcher-matched entries.

### Piping to notification tools

Use `stdout` JSON events and pipe `nosygit` output into downstream tools such as [projectdiscovery/notify](https://github.com/projectdiscovery/notify).

```sh
nosygit run poll | jq -r '.message' | notify -silent
```

Example with filtering, tagging, and line buffering:

```sh
nosygit run poll \
  | jq -rc 'select(.kind == "watcher" and .event == "commit-message") | "[nosygit] " + .message' \
  | stdbuf -oL cat \
  | notify -provider-config ~/.config/notify/provider-config.yaml
```
