# Commands

<p align="center">
  <a href="#nosygit-run-poll"><code>run poll</code></a> •
  <a href="#nosygit-run-listen"><code>run listen</code></a> •
  <a href="#nosygit-fetch"><code>fetch</code></a> •
  <a href="#nosygit-process"><code>process</code></a> •
  <a href="#nosygit-state-list"><code>state list</code></a>
</p>

All commands are run from a directory containing `config.yml`. State is persisted to `state.db` in the same directory.

**Global Flags**

| Flag | Description |
|---|---|
| `-v`, `--verbose` | Emit verbose runtime meta events (session init, fetch progress, repo sync steps). Useful for debugging. |
| `-p`, `--provider <name>` | Limit to one named provider. Bypasses the `poll_every` schedule check where applicable. |
| `-a`, `--all` | Include all providers (default behaviour for most commands). |

**Environment**

| Variable | Required | Description |
|---|---|---|
| `GITHUB_TOKEN` | For GitHub providers | Personal access token with `repo` scope (or `read:org` for org-wide listing). |
| `GITLAB_TOKEN` | For GitLab providers | Personal access token with `read_api` scope. |

---

## `nosygit run poll`

Queries providers for repository and pull/merge request changes, evaluates watchers against new commits, and emits events. Writes updated state on completion. Providers with `poll_every: never` are skipped entirely (unless `--provider` targets them explicitly).

By default runs once and exits, making it safe to call from a cron job or external scheduler.

| Flag | Description |
|---|---|
| `--daemon` | Keep running and re-poll on a loop. Sleeps between runs for the shortest `poll_every` interval across all providers, with a minimum of 60 seconds. |

**Default (run once, all due providers):**

```sh
nosygit run poll
```

Suitable for cron:

```sh
*/5 * * * * cd /opt/nosygit && nosygit run poll >> events.jsonl 2>&1
```

**Target one provider (bypasses schedule):**

```sh
nosygit run poll --provider github-main
```

**Daemon mode:**

```sh
nosygit run poll --daemon
```

Sleeps between runs based on the shortest explicit `poll_every` in the config, never faster than 60 seconds. Providers with `poll_every` omitted run on every tick; providers with `poll_every: never` are always skipped.

**What it does, in order:**

1. Loads `config.yml` and `state.db`
2. Determines which providers are due based on `poll_every` and last poll timestamp (bypassed with `--provider`)
3. For each due GitHub provider: lists repositories, clones or pulls each, syncs pull requests
4. For each due GitLab provider: lists projects, clones or pulls each, syncs merge requests
5. For each due Git provider: clones or pulls the configured URL
5. Evaluates all configured watchers against new commits (file, regex, commit-message, contributor, branch, change-request, etc.)
6. Emits matched watcher events to configured sinks
7. Writes updated state and last-polled timestamps

---

## `nosygit run listen`

Starts a long-running HTTP server that receives incoming webhook deliveries from GitHub and GitLab. Each provider defined in `config.yml` gets its own endpoint:

```
POST /webhook/<provider-name>
```

The server validates each delivery's signature or token, records it idempotently in `state.db`, then runs the same watcher evaluation pipeline as `nosygit run poll`. Push events trigger a full git fetch and commit analysis; pull request / merge request events update change request state.

| Flag | Description |
|---|---|
| `--port <n>` | Port to listen on. Overrides `webhook.port` in `config.yml`. Default: `8080`. |
| `--host <addr>` | Address to bind to. Overrides `webhook.host` in `config.yml`. Default: all interfaces. |

```sh
nosygit run listen
nosygit run listen --port 9090
nosygit run listen --host 127.0.0.1 --port 8080
nosygit run listen --provider github-main
```

**Webhook URL registration:**

Register the endpoint in your provider's webhook settings. The URL format is:

```
https://<your-host>:<port>/webhook/<provider-name>
```

For example, with provider key `github-main`:

```
https://hooks.example.com:8080/webhook/github-main
```

**Relation to polling:**

`nosygit run listen` and `nosygit run poll` can run in parallel. All state writes are idempotent — duplicate deliveries and polling overlap are handled safely. To run a provider in webhook-only mode and exclude it from polling, set `poll_every: never` in its provider config.

See [configuration.md — Webhook Server](./configuration.md#webhook-server) for full event type coverage and config options.

---

## `nosygit fetch`

Discovers and clones (or pulls) repositories from configured providers **without** running watchers or emitting events. Useful for pre-seeding the clone directory before the first `run poll`, or refreshing clones in bulk.

```sh
nosygit fetch --all
nosygit fetch --provider github-main
```

Repositories are cloned to `clone_path/<derived-name>` (default: `/tmp/nosygit/`). Already-cloned repositories are pulled to the latest state. Corrupt clones are removed and re-cloned.

---

## `nosygit process`

Fetches from origin and re-evaluates commits against all configured watchers for state-known repos, emitting events for matches. Does not interact with any provider API — use this to replay watcher evaluation after a config change without waiting for the next poll.

Requires `--provider` or `--all`.

**Process all repos for a provider:**

```sh
nosygit process --provider github-main
```

**Process all repos:**

```sh
nosygit process --all
```

---

### `nosygit state list`

Lists all repositories in persisted state with their stored head commit SHA and first line of the commit message.

| Flag | Description |
|---|---|
| `-p`, `--provider <name>` | Filter output to repositories belonging to this provider. |
| `--prefix <prefix>` | Filter output to repository names starting with this prefix. |

```sh
nosygit state list
nosygit state list --provider github-main
nosygit state list --prefix acme/
```

**Typical use:** inspect what head each repo is at before running `nosygit process` manually, or verify state after a migration.
