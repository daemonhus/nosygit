<h1 align="center">
   | ˚ㄥ˚ / 
  <br>
</h1>

<p align="center">
<a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/license-MIT-_red.svg"></a>
</p>

<p align="center">
  <a href="#documentation">Documentation</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="examples/">Examples</a>
</p>

Nosygit is the preeminent stalker of Git repos - a daemon for executing actions based on changes observed.

Bring visibility to events like new repositories / branches, commit message regex patterns matched, new contributors' first commits, and much more!

Event sink output is structured for consistent downstream parsing and alerting, to: stdout and file - a stream of all things Git.

- ✅️ Git provider agnostic, with discovery across: GitHub and GitLab
- ✅️ Tracks stateful heuristics (threshold across many commits) for contextual monitoring
- ✅️ Supports daemon-based polling and incoming webhooks: multiple deployment models 

# Documentation

- [Commands](docs/cmds.md)
- [Configuration](docs/configuration.md)
- [Events](docs/events.md)

# Quick Start

**1. Install**

```sh
go install -v github.com/daemonhus/nosygit/cmd/nosygit@latest
```

**2. Set up credentials**

```sh
export GITHUB_TOKEN=<your-github-pat>   # if using a GitHub provider
export GITLAB_TOKEN=<your-gitlab-pat>   # if using a GitLab provider
```

**3. Create a config file**

Copy the example config and edit it for your organisation:

```sh
cp examples/.config.yml ./config.yml
```

**4. Run**

```sh
nosygit run poll
```

Events will be emitted to stdout (or any sinks configured in `config.yml`). Use `nosygit run poll --daemon` to keep running continuously. For full CLI options and configuration examples, see the docs above.

# Docker

**1. Prepare mounts**

```sh
touch state.db   # must exist before mounting — Docker will create it as a directory otherwise
```

The `repos` directory is created automatically by nosygit; you do not need to create it in advance.

**2. Run**

```sh
docker run \
  -v $(pwd)/config.yml:/app/config.yml \
  -v $(pwd)/state.db:/app/state.db \
  -v $(pwd)/repos:/app/repos \
  -v ~/.ssh/id_ed25519:/home/nonroot/.ssh/id_ed25519:ro \
  -e GITHUB_TOKEN=$GITHUB_TOKEN \
  -e GITLAB_TOKEN=$GITLAB_TOKEN \
  ghcr.io/daemonhus/nosygit:latest run poll --daemon
```

State and cloned repositories are persisted to the host so they survive container restarts. Set `clone_path: /app/repos/` in your `config.yml` to match the mount above.

# Development

```sh
make build
make test
```

Set up local pre-commit checks (same gates as CI: tidy, fmt, vet, test, build):

```sh
brew install pre-commit
pre-commit install
pre-commit run --all-files
```
