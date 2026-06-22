# Dashboard

This is a personal dashboard, mostly created with Claude Code. Meant to replace many other productivity apps to have everything in one place.

Runs on Go and Docker Compose.

## Run

```bash
cp config.yaml.example config.yaml
# Edit config.yaml with your settings
DASHBOARD_NAME=Franco docker compose up --build
```

Open [http://localhost:8080](http://localhost:8080).

## Config

`config.yaml` controls which plugins are enabled and how they behave. The file supports `$ENV_VAR` substitution, so secrets can stay in the environment.

See [`config.yaml.example`](./config.yaml.example) for all available options.

## Plugins

| Plugin | What it shows |
|---|---|
| `github` | Open PRs and issues involving you |
| `notifications` | Unread GitHub notifications |
| `releases` | Latest releases for watched repos |
| `calendar` | Events from iCal/WebCal URLs |
| `tasks` | Local to-do list (persisted in a Docker volume) |
| `status` | Status of compatible services |
