# KubexHealthCheck

A small internal tool: type a message into a web page, and it either answers via AWS Bedrock, or runs a Kubernetes fleet health check across every configured Kubex customer — either way, the answer is also posted to a Teams webhook.

## Backend

A Go web service (`backend/`), no framework beyond the standard library (`net/http`).

Setup:

```bash
cd backend
go build .
```

Every endpoint requires a shared-secret API key. Send it as an `X-Api-Key` header on every request; requests without it, or with the wrong value, get `401`.

Configure `appsettings.Development.json` (or environment variables — same `Section__Key` names either way, e.g. `ApiKeySettings__Key`):

- `ApiKeySettings:Key` — the shared secret. Generate one yourself (e.g. `openssl rand -hex 24`) and put the same value here, in the frontend's `API_KEY` constant, and in the `API_KEY` GitHub Actions secret. If this is left empty, the server responds `500` on every request rather than silently allowing unauthenticated access.
- `WebhookSettings:DefaultUrl` — the Teams webhook URL every response gets posted to. Since this URL itself contains a secret signature, don't commit your real value — fill it in locally only, or set it via the `WebhookSettings__DefaultUrl` environment variable.
- `BedrockSettings:ApiKey` / `BedrockSettings:Region` / `BedrockSettings:ModelId` — your AWS Bedrock credentials. `ApiKey` is a Bedrock API key (the bearer-token style, `Authorization: Bearer ...` — not IAM credentials). `Region` is the AWS region the model is enabled in (e.g. `us-east-1`). `ModelId` is Bedrock's own model identifier (e.g. `us.anthropic.claude-opus-4-5-20251101-v1:0`).
- `CustomersFile` (optional) — path to the fleet customer list. Defaults to `customers.csv` next to the binary. See "Fleet health check" below.

Run:

```bash
go run .
```

### API

- `GET /healthz` — unauthenticated; just confirms the process is up.
- `POST /api/claude/command` — body `{ "command": "..." }`. Runs the command (see "Commands" below), posts the result to the configured Teams webhook, and returns it. Response body: `{ response, postedToTeams, postError }`.

## Commands

Everything goes through one endpoint and one text field — there are only two shapes of command:

### A plain message

Anything that doesn't contain "health check" (e.g. `hi`, or a real question) goes straight to Bedrock as a free-form prompt, and the answer comes back as plain text.

A typed word that matches an installed skill (a folder under `skills/` — e.g. `onthisday`) runs that skill's instructions instead of a generic prompt.

### `run health check` (or anything containing "health check")

Runs the `kubex-health-check` skill once per customer listed in `backend/customers.csv`, combining every customer's one-line answer into a single message.

`backend/customers.csv` (gitignored — see `customers.csv.example` for the shape) is a plain four-column CSV: name, MCP URL, username, password.

```csv
name,mcpUrl,username,password
sandbox,https://sandbox-mcp.kubex.ai,your-densify-username,your-densify-password
sandboxuat,https://sandboxuat-mcp.kubex.ai,your-densify-username,your-densify-password
```

The header row is optional — it's detected and skipped automatically (a first row whose column B doesn't start with `http` is treated as a header).

**How each customer's data actually gets fetched**: Kubex's plain REST login (`POST {plain-host}/api/v2/authorize`, username/password → JWT) and its MCP server are two different credential systems — a plain login token is rejected outright by the MCP server's regular `/mcp` endpoint. Confirmed by testing, though, that the MCP server's `/mcp-token` endpoint *does* accept this same login token directly (no separate MCP OAuth flow needed). So for each customer: sign in via `internal/kubexauth` (host derived from the `mcpUrl` column by stripping `-mcp`, e.g. `sandboxuat-mcp.kubex.ai` → `sandboxuat.kubex.ai`), then call the real `kubex-cluster-connections` MCP tool via `internal/kubexmcp` against `{mcpUrl}/mcp-token` using that token. The real tool result is handed to Bedrock alongside the skill's instructions in a single call — no back-and-forth tool-use loop needed, since the data's already fetched before Bedrock is ever called.

Customers are queried concurrently, capped at 5 at once (`fleetConcurrency` in `internal/claude/service.go`). If a customer's call fails (bad credentials, connection issue, etc.), that one shows up as `<name>: FAILED - <reason>` in the combined message instead of failing the whole report.

## Skills

`skills/<name>/SKILL.md` — a folder is a skill, no code change needed to add one. `skills/kubex-health-check/SKILL.md` is the one the fleet command runs; `skills/onthisday/SKILL.md` is a simple standalone example with no Kubex dependency at all.

Every skill dispatch is automatically given the current date (US Eastern) as a short context line before the instruction, since Bedrock has no clock of its own.

## Frontend

`frontend/index.html` is a single static page (no build step) — open it directly in a browser, or serve it with any static file server. One command box that calls `POST /api/claude/command`. Edit the `API_BASE_URL`/`API_KEY` constants at the top of the page's `<script>` — there are no input fields for them, since every secret this service needs already lives server-side.

## Scheduled / on-demand runs (GitHub Actions)

Both workflows build and start the backend in the Actions runner, send one command, and let the backend post the result to Teams itself — no server needs to stay running between runs.

- **`.github/workflows/good-morning.yml`** — runs on a daily cron (`workflow_dispatch` also lets you trigger it on demand from the Actions tab). Sends a fixed free-form prompt by default; this is also the natural place to eventually point at `run health check` once you want the fleet report running on a schedule instead of manually.
- **`.github/workflows/claude-command.yml`** — `workflow_dispatch`-only, takes a `command` input and sends whatever you pass in. Useful for triggering a one-off run (including `run health check`) without keeping any server of your own running.

Repository secrets needed (GitHub repo → Settings → Secrets and variables → Actions):

- `API_KEY` — same as `ApiKeySettings:Key`
- `TEAMS_WEBHOOK_URL` — same as `WebhookSettings:DefaultUrl`
- `BEDROCK_API_KEY` / `BEDROCK_REGION` / `BEDROCK_MODEL_ID` — same as `BedrockSettings:ApiKey`/`Region`/`ModelId`

Trigger either workflow manually from the Actions tab, or dispatch `claude-command.yml` from elsewhere via the GitHub API:

```
POST https://api.github.com/repos/Demqnd/KubexHealthCheck/actions/workflows/claude-command.yml/dispatches
Authorization: Bearer <a GitHub personal access token>
Accept: application/vnd.github+json
Content-Type: application/json

{
  "ref": "main",
  "inputs": {
    "command": "run health check"
  }
}
```

The token needs `Actions: Read and write` permission — use a fine-grained personal access token scoped to just this repository. The call returns `202 Accepted` immediately; it doesn't wait for the workflow to finish. Each run does a full `go build` from scratch, so expect roughly 30-60 seconds between triggering it and seeing a reply in Teams.
