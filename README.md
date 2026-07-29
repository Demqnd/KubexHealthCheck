# KubexHealthCheck

A standalone webhook routine service: configure a webhook URL and send it messages on demand. Originally built as an admin feature inside expenseDensify, extracted here as its own project. Tested against a Microsoft Teams "Workflows" incoming webhook, which requires messages wrapped as an Adaptive Card attachment — `WebhookMessageSender` builds that shape automatically.

## Backend

ASP.NET Core Web API (`backend/`), .NET 10, PostgreSQL via EF Core.

Setup:

```bash
cd backend
dotnet restore
```

Configure `appsettings.Development.json` (or environment variables):

- `ConnectionStrings:DefaultConnection` — Postgres connection string.
- `ApiKeySettings:Key` — a secret value clients must send in the `X-Api-Key` header on every request.
- `KubexApiSettings:BaseUrl` — your Kubex dashboard URL (e.g. `https://your-instance.kubex.ai`), used for the Kubex health check feature.
- `KubexApiSettings:Username` / `KubexApiSettings:Password` — credentials for an **API-enabled** Kubex user (see Kubex's `POST /authorize` docs). Required only for `POST /api/kubexhealthcheck/run`.

Run:

```bash
dotnet run
```

On startup it creates the `webhook_routines` table if it doesn't exist.

### API

All endpoints require an `X-Api-Key` header matching `ApiKeySettings:Key`.

- `GET /api/webhook` — returns the currently configured webhook URL.
- `PUT /api/webhook` — body `{ "url": "https://..." }`, saves the webhook URL.
- `POST /api/webhook/send` — body `{ "message": "..." }`, sends the message to the configured webhook.
- `POST /api/kubexhealthcheck/run` — authenticates against the Kubex REST API (`KubexApiSettings`), fetches cluster health via `GET /kubernetes/clusters`, builds a summary (node/container counts, Kubernetes version, data freshness per cluster), and posts it to the configured webhook. Requires `KubexApiSettings` to be fully configured; returns `502` with a descriptive message otherwise.

## Frontend

`frontend/index.html` is a single static page (no build step) — open it directly in a browser, or serve it with any static file server. Fill in the API base URL and API key, then use it to save the webhook URL and send messages.
