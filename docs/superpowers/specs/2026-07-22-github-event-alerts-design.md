# GitHub Event Push Alerts — Design

Date: 2026-07-22

## Goal

Actively **push** an alert (outside the passive dashboard list) when a new,
actionable GitHub event appears. The dashboard already *displays* unread
notifications; this adds delivery to **browser notifications** and **ntfy** when
new events show up.

## Non-goals

- Real-time streaming (backend data only changes on the 5m refresh, so polling
  is sufficient).
- New event sources beyond what plugins already fetch.
- Releases do not trigger alerts.

## Architecture

New `internal/alerts` package with a `Notifier`, wired in `main` and invoked by
the scheduler after every `RefreshAll`. Plugins remain pure data sources; the
notifier *consumes* the registry's item snapshot. This keeps alerting
cross-plugin and touches no plugin internals.

### Data flow (per scheduler tick)

```
RefreshAll → registry.Items() snapshot
  → notifier.Process(items):
       filter   → keep only alertable items (rules + exclusions)
       diff     → IDs not in seen-set = new
       dispatch → send each new Alert to every enabled channel
       persist  → add IDs to seen-set (state_path)
```

`Process` is kept pure: dispatch + persistence are injected so it is unit-testable.

## Alertable items (rules)

- `notifications` plugin → any item.
- `github` plugin → items whose `metadata.categories` contains
  `review_requested` or `assigned`.
- Each maps to `Alert{Title, URL, Repo, Author, Reason}`.

Rules are individually toggleable via config (`notifications`,
`review_requests`, `assigned_issues`).

## Exclusion filter

- `exclude_authors` — matches `metadata.author`. Note: notification items carry
  no author field, so author-exclusion only affects PR/issue alerts (which is
  where bots like `dependabot[bot]` appear anyway).
- `exclude_repos` — matches repository; applies to all sources.

## Seen-state & startup

- Persisted as JSON to `state_path` (default `/data/alerts.json`, the Docker
  volume — same pattern as `tasks.json`).
- **Cold-start baseline**: on first run (no state file), all currently-present
  alertable IDs are seeded as "seen" silently, so the existing backlog never
  floods on startup/restart. Only items appearing after baseline alert.
- IDs no longer present in the snapshot are pruned to bound file size.

## Channels

Interface: `Channel{ Name() string; Send(ctx, Alert) error }`.

- **ntfy** — HTTP POST to `<server>/<topic>` with title, body, and a
  click-through URL header. Config: `server` (default `https://ntfy.sh`),
  `topic`. Uses an `http.Client` with a timeout.
- **browser** — appends to a capped in-memory ring buffer (TTL ~15m), exposed at
  `GET /api/alerts?since=<cursor>`. Dashboard JS requests Notification
  permission once, polls every 60s, fires `new Notification()` per alert,
  advances a cursor in `localStorage`; clicking opens the URL.

A channel `Send` failure is logged and never blocks other channels or the refresh.

## Config

New top-level `alerts:` section (cross-plugin, not under `plugins:`).

```yaml
alerts:
  enabled: true
  state_path: "/data/alerts.json"
  rules: { notifications: true, review_requests: true, assigned_issues: true }
  exclude_authors: ["dependabot[bot]", "renovate[bot]"]
  exclude_repos: []
  channels:
    browser: { enabled: true }
    ntfy:    { enabled: true, server: "https://ntfy.sh", topic: "${NTFY_TOPIC}" }
```

Misconfigured/disabled alerts → feature off with a logged warning, never a crash.

## Wiring

- `config.go`: add `Alerts AlertsConfig` to root `Config`.
- `main.go`: build `Notifier` (+ enabled channels) from `cfg.Alerts` after
  plugins register; pass the browser buffer into `api.NewServer`; pass the
  notifier into `scheduler.New`.
- `scheduler.go`: after `RefreshAll`, call `notifier.Process(registry.Items(...))`.
- `server.go`/`handlers.go`: add `GET /api/alerts` reading the browser buffer.
- `dashboard.html`: add browser-notification JS.

## Testing

- `notifier`: table tests — new-vs-seen diff, exclusions, cold-start baseline,
  rule mapping from metadata.
- `ntfy` channel: `httptest` server, assert POST payload + headers.
- `browser` buffer: enqueue / cursor / TTL / cap behavior.
