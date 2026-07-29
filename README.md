# KubexHealthCheck

A standalone webhook routine service: configure a webhook URL and send it messages on demand. Originally built as an admin feature inside expenseDensify, extracted here as its own project. Tested against a Microsoft Teams "Workflows" incoming webhook, which requires messages wrapped as an Adaptive Card attachment — `WebhookMessageSender` builds that shape automatically.

## Backend

ASP.NET Core Web API (`backend/`), .NET 10. No database — the webhook URL is persisted to a small JSON file (`backend/data/webhook-routine.json`, gitignored) next to the project, so there's nothing to install or run besides the .NET SDK.

Setup:

```bash
cd backend
dotnet restore
```

There's no authentication on the API — it's meant to run locally / on a trusted network only. Don't expose it to the public internet as-is.

Configure `appsettings.Development.json` (or environment variables):

- `KubexApiSettings:BaseUrl` — your Kubex dashboard URL (e.g. `https://your-instance.kubex.ai`), used for the Kubex health check feature.
- `KubexApiSettings:Username` / `KubexApiSettings:Password` — credentials for an **API-enabled** Kubex user (see Kubex's `POST /authorize` docs). Required only for `POST /api/kubexhealthcheck/run`.
- `ClaudeApiSettings:ApiKey` — an Anthropic API key. Optional: if set, `POST /api/kubexhealthcheck/run` sends the raw cluster data to Claude for an AI-written summary instead of the plain formatted one. If unset (or the call fails), it falls back to the deterministic summary automatically.
- `ClaudeApiSettings:Model` — defaults to `claude-opus-5`.
- `DataDirectory` (optional) — where to store `webhook-routine.json`. Defaults to a `data/` folder next to the project.

Run:

```bash
dotnet run
```

### API

- `GET /api/webhook` — returns the currently configured webhook URL.
- `PUT /api/webhook` — body `{ "url": "https://..." }`, saves the webhook URL.
- `POST /api/webhook/send` — body `{ "message": "..." }`, sends the message to the configured webhook.
- `POST /api/kubexhealthcheck/run` — authenticates against the Kubex REST API (`KubexApiSettings`), fetches cluster health via `GET /kubernetes/clusters`, and posts a summary to the configured webhook. If `ClaudeApiSettings:ApiKey` is set, the summary is AI-written (Claude reads the raw cluster JSON and writes a short plain-text summary); otherwise — or if the Claude call fails — it falls back to a deterministic summary (node/container counts, Kubernetes version, data freshness per cluster). The response body's `usedAi` field tells you which one was used. Requires `KubexApiSettings` to be fully configured; returns `502` with a descriptive message otherwise.
- `POST /api/claude/ask` — body `{ "apiKey": "sk-ant-...", "question": "...", "postToTeams": true|false }`. Sends the question straight to Claude using the API key **you pass in the request** (not `ClaudeApiSettings` — this endpoint is for ad-hoc testing without touching config at all). If `postToTeams` is true, also posts the answer to the configured webhook. The key is used only for that one call and never persisted.

## Frontend

`frontend/index.html` is a single static page (no build step) — open it directly in a browser, or serve it with any static file server. Fill in the API base URL, then use it to save the webhook URL, send messages, run the Kubex health check, or ask Claude a one-off question (pasting your own Anthropic API key into that form).

## Scheduled health checks (GitHub Actions)

`.github/workflows/kubex-health-check.yml` runs the health check on a cron schedule (daily by default — edit the `cron:` line to change it) without needing any server or computer left running. Each run: checks out the repo, builds and starts the backend in the Actions runner, configures the webhook URL, calls `POST /api/kubexhealthcheck/run`, then tears everything down.

Add these as **repository secrets** (GitHub repo → Settings → Secrets and variables → Actions → New repository secret):

- `KUBEX_BASE_URL`, `KUBEX_USERNAME`, `KUBEX_PASSWORD` — same as `KubexApiSettings` locally
- `CLAUDE_API_KEY` — optional, for the AI-written summary
- `TEAMS_WEBHOOK_URL` — your Teams webhook URL

You can trigger it on demand from the repo's **Actions** tab (it has `workflow_dispatch` enabled) instead of waiting for the schedule, to test it right after setting up secrets.
