# Usage

Install and run:

```sh
go install -v github.com/daemonhus/nosygit/cmd/nosygit@latest
nosygit -h
```

## Command Overview

| Command | Purpose |
|---|---|
| `start` | Run the daemon flow: poll providers, detect changes, and emit events. |
| `clone` | Clone repositories from configured providers without running watchers/events. |
| `git <path> [from-commit]` | Process a local repository path and evaluate new commits/watchers. |
| `github <provider>` | Poll a single configured GitHub provider and update state. |
| `gitlab <provider>` | Poll a single configured GitLab provider and update state. |
| `state list` | Inspect persisted repository state. |

Global flags:

| Flag | Description |
|---|---|
| `-v`, `--verbose` | Enable verbose meta event output. |

## Common prerequisites

- Run commands from a directory containing `./config.yml`.
- State is stored in `./state.db`.
- Event output is JSONL (`v1`) on configured sinks (`stdout`, `file-append`).

## Command Reference

### `nosygit start`

Runs the main daemon workflow:

- loads config and state
- polls configured providers (respecting `poll_every`)
- fetches/pulls repository updates
- runs watcher evaluation
- emits `watcher`, `remote`, and `meta` events
- writes updated state

Example:

```sh
nosygit start
```

### `nosygit clone`

Clones repositories only (no watcher/event execution).

Flags:

| Flag | Description |
|---|---|
| `-a`, `--all` | Clone from all configured providers. |
| `-p`, `--provider <name>` | Clone from one configured provider. |

Examples:

```sh
nosygit clone --all
nosygit clone --provider gh-main
```

### `nosygit git <path> [from-commit]`

Runs commit selection and watcher evaluation for one local repository path.

- `path`: local path to the repository
- `from-commit` (optional): override starting commit/head baseline

Examples:

```sh
nosygit git /tmp/nosygit/acme/api
nosygit git /tmp/nosygit/acme/api a1b2c3d4
```

### `nosygit github <provider>`

Runs provider sync for a single GitHub provider configured in `providers`.

Example:

```sh
nosygit github gh-main
```

### `nosygit gitlab <provider>`

Runs provider sync for a single GitLab provider configured in `providers`.

Example:

```sh
nosygit gitlab gl-main
```

### `nosygit state list`

Lists all repositories in persisted state with their current head commit and message.

Flags:

| Flag | Description |
|---|---|
| `-p`, `--provider <name>` | Filter by provider name. |
| `--prefix <prefix>` | Filter by repository path prefix. |

Example:

```sh
nosygit state list
nosygit state list --provider gh-main
nosygit state list --prefix acme/
```

## Operational Notes

- Repository polling uses fetch/pull semantics and can optionally run in shallow mode.
- Branch lifecycle watchers (`type: branch`) are evaluated from remote ref changes during fetch.
- See `docs/configuration.md` for full watcher and shallow config examples.
- See `docs/events.md` for the event output contract.
