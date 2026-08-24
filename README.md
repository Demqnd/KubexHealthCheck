# KubexHealthCheck

A standalone webhook routine service: configure a webhook URL and send it messages on demand. Originally built as an admin feature inside expenseDensify, extracted here as its own project. Tested against a Microsoft Teams "Workflows" incoming webhook, which requires messages wrapped as an Adaptive Card attachment — `WebhookMessageSender` builds that shape automatically.

## Backend

A Go web service (`backend/`), no framework beyond the standard library (`net/http`). No database — the webhook URL is persisted to a small JSON file (`backend/data/webhook-routine.json`, gitignored) next to the project, so there's nothing to install or run besides the Go toolchain.

Setup:

```bash
cd backend
go build .
```

Every endpoint requires a shared-secret API key, since this service is meant to be reachable over the public internet (e.g. by a Teams bot). Send it as an `X-Api-Key` header on every request; requests without it, or with the wrong value, get `401`.

Configure `appsettings.Development.json` (or environment variables — same `Section__Key` names either way, e.g. `ApiKeySettings__Key`):

- `ApiKeySettings:Key` — the shared secret. Generate one yourself (e.g. `openssl rand -hex 24`) and put the same value here, in the frontend's "API key" field, and in the `API_KEY` GitHub Actions secret. If this is left empty, the server responds `500` on every request rather than silently allowing unauthenticated access.
- `WebhookSettings:DefaultUrl` — optional. If set, this becomes the webhook URL used by `/api/webhook/send` and `/api/claude/command`, with no `PUT /api/webhook` call needed first — useful so a fresh clone works immediately without a manual setup step. `PUT /api/webhook` still works as before and overrides this once called (the override is saved to `webhook-routine.json` and takes precedence from then on). Since this URL itself contains a secret signature, don't commit your real value — fill it in locally only, or set it via the `WebhookSettings__DefaultUrl` environment variable, same as `ApiKeySettings:Key`.
- `ClaudeApiSettings:ApiKey` — an Anthropic API key. Used by `POST /api/claude/command` (the bot-facing command endpoint) and `POST /api/claude/ask`.
- `ClaudeApiSettings:Model` — defaults to `claude-opus-5`.
- `KubexMcpSettings:AuthorizationToken` — optional. Lets a bot command connect Claude to a Kubex MCP server for that one request — see "MCP-connected commands" below.
- `DataDirectory` (optional) — where to store `webhook-routine.json`. Defaults to a `data/` folder next to the project.
- `CustomersFile` (optional) — path to the fleet-report customer list. Defaults to `customers.csv` next to the binary. See "Fleet reports" below.

Run:

```bash
go run .
```

### API

- `GET /api/webhook` — returns the currently configured webhook URL.
- `PUT /api/webhook` — body `{ "url": "https://..." }`, saves the webhook URL.
- `POST /api/webhook/send` — body `{ "message": "..." }`, sends the message to the configured webhook.
- `POST /api/claude/ask` — body `{ "apiKey": "sk-ant-...", "question": "...", "postToTeams": true|false }`. Sends the question straight to Claude using the API key **you pass in the request** (not `ClaudeApiSettings` — this endpoint is for ad-hoc testing without touching config at all). If `postToTeams` is true, also posts the answer to the configured webhook. The key is used only for that one call and never persisted.
- `POST /api/claude/command` — body `{ "command": "..." }`. The bot-facing endpoint: send whatever a Teams/bot user typed, get back Claude's response. Unlike `/ask`, this uses the server-configured `ClaudeApiSettings:ApiKey`, not one passed in the request — it's meant to be called by an integration (e.g. a Power Automate flow) that only knows the shared `X-Api-Key`, not an Anthropic key. It **always** also posts the response to the configured webhook (same one used by `/api/webhook/send`), so a caller doesn't need any separate "post back to Teams" step. Response body: `{ response, postedToTeams, postError }` — `response` is always populated (or the call itself returns `502` if Claude failed); `postedToTeams`/`postError` report whether the webhook post succeeded.

All endpoints above require the `X-Api-Key` header described in Setup.

### Two-way Teams bot (Power Automate)

To let someone type a message in Teams and get a Claude-generated reply back, without registering a real Azure Bot: build a Power Automate flow with

1. A trigger that fires on a Teams message (e.g. "When a keyword is mentioned" or "When a new channel message is added")
2. A single **HTTP** action: `POST https://<your-public-host>/api/claude/command`, header `X-Api-Key: <shared secret>`, body `{ "command": "<the message text>" }`

That's it — no third action needed, since the backend itself posts the answer back into the same Teams channel via the webhook. Note that Power Automate's HTTP action is a **Premium** connector, so your M365 tenant needs Power Automate Premium (or a per-user/per-flow license) for this to work.

### MCP-connected commands

If a `command` sent to `POST /api/claude/command` contains a URL (e.g. `@KubexAI https://sandboxuat-mcp.kubex.ai kubex-health-check`), the backend automatically connects Claude to that URL as an MCP server for that one request — using the [Anthropic MCP connector](https://platform.claude.com/docs/en/agents-and-tools/mcp-connector), Claude can call tools exposed by that server before answering. The URL is stripped out of the text sent to Claude; what's left is checked against the skill registry the same way as the no-URL case below:

- **A skill word right after the URL** (e.g. `kubex-health-check`) selects that skill by name — its `SKILL.md` becomes the system prompt, and the *remaining* text (if any) becomes the instruction. This is what lets the same skill run against any connected client's MCP endpoint — `@KubexAI https://sandboxuat-mcp.kubex.ai kubex-health-check` and `@KubexAI https://fedex-mcp.kubex.ai kubex-health-check` run the identical logic against two different fleets, just by swapping the URL.
- **No recognized skill word** (e.g. `@KubexAI https://some-mcp-server.example.com/mcp check cluster status`) falls back to `skills/kubex-health-check/SKILL.md` by default — this is the original behavior, kept for backward compatibility with plain "just a URL" commands.
- Unlike the no-URL case below, a skill marked `dispatch:false` **is** usable here — that marker only blocks the plain word-dispatch path (which has no way to attach an MCP server); once a URL is already present and about to be attached, the thing the marker was guarding against no longer applies.

The MCP server's own auth token comes from the server-side `KubexMcpSettings:AuthorizationToken` config (not from the message) — so a bot user only ever needs to include the MCP server's URL, never a credential, in their Teams message. This keeps the token out of Teams chat history and Power Automate run logs. Note this token is shared across *every* MCP URL a command supplies — there's no per-client credential lookup, so it's on you to make sure whatever's configured actually has access to every fleet you plan to query this way.

### Fleet reports (multiple customers, one Teams message)

The single-URL command above needs one shared `KubexMcpSettings:AuthorizationToken` that has to work against every MCP URL you supply — fine for one or two clients, not for querying many customers who each issued you their own token. `fleet <skillword> [instruction]` (e.g. `@KubexAI fleet kubex-cluster-count`) is the multi-customer version: it runs that skill once per customer listed in `backend/customers.csv`, **each with its own MCP URL and its own auth token**, and combines every customer's answer into a single message — one post to Teams, not one per customer.

`backend/customers.csv` (gitignored — see `customers.csv.example` for the shape) is a plain two-column CSV: MCP URL in column A, authorization token in column B.

```csv
url,authorizationToken
https://sandbox-mcp.kubex.ai,your-real-token-here
https://sandboxuat-mcp.kubex.ai,your-other-real-token-here
```

The header row is optional — it's detected and skipped automatically (a first column that doesn't start with `http` is treated as a header), so the file works with or without one. There's no separate name column; each customer's display name in the combined report is just derived from the URL's host (e.g. `sandbox-mcp.kubex.ai: 14 clusters connected.`).

Notes:

- Customers are queried concurrently, capped at 5 at once (`fleetConcurrency` in `internal/claude/service.go`) so a 40-customer run doesn't fire 40 simultaneous requests at Anthropic.
- If a customer's call fails (bad/expired token, wrong host, etc.), that one shows up as `<name>: FAILED - <reason>` in the combined message instead of failing the whole report — one bad token doesn't block everyone else's results.
- Like the MCP-connected path above, `dispatch:false` doesn't block a skill here — an MCP server is being attached per customer either way.
- The skill's own `<!-- model:... -->` override (if any) still applies, same as the single-URL path.
- Right now each customer's answer is just joined line by line. Feeding all of them into one more Claude call to produce a single synthesized report (instead of a plain per-customer list) is the natural next step here — `RunFleet` in `internal/claude/service.go` is where that would slot in, without changing how the fan-out itself works.

### Word-dispatched skills

If a `command` has no MCP URL in it, the backend checks whether its first word names an installed skill — a folder under `skills/` containing a `SKILL.md`. If it matches (case- and punctuation-insensitive, and tolerant of a leading `@KubexAI` mention) **and isn't marked `dispatch:false`**, that file's contents become the system prompt for the request, and everything after the skill word becomes the instruction sent to Claude. If nothing matches, the command is treated as a plain free-form question, same as before.

**Adding a new skill is just adding a folder** — no code change, no config edit, no restart logic to wire up:

```
skills/
  onthisday/
    SKILL.md      # dispatched as "@KubexAI onthisday"
  kubex-health-check/
    SKILL.md      # full status/freshness/version-drift summary
  kubex-cluster-count/
    SKILL.md      # model:claude-haiku-4-5-20251001 — just the cluster count, on a cheaper model
```

- The dispatch word is the folder name. `onthisday`, `OnThisDay`, and `onthisday?` (trailing punctuation from a Teams message) all resolve to the same skill.
- `SKILL.md`'s full contents are used as-is as the system prompt — write it the same way you'd write instructions for a Claude Code skill (see `skills/onthisday/SKILL.md` for the pattern: what it does, how to read the invocation, step-by-step rules, and the exact output style). `skills/kubex-health-check/SKILL.md` is a more advanced example: one file, written to work correctly in *two* different contexts (interactive Claude Code/Desktop, and this backend) by explicitly branching on which one applies at the one step where they differ.
- Every skill dispatch is automatically given the current date (`US Eastern`) as a short context line before the instruction, since Claude has no clock of its own — a skill that needs "today" (like `onthisday`) can just say so in its instructions and rely on that context being there.
- Marker lines are read from a leading block of `<!-- ... -->` HTML comments at the top of the file (reading stops at the first non-comment line), so a skill can carry more than one:
  - `<!-- dispatch:false -->` means: **not usable from this plain word-dispatch path**, because that logic needs something code-level this path can't provide — an MCP server actually attached to the request being the main case. It does *not* mean "never used" — see MCP-connected commands above, where the same skill is fully usable once a URL supplies the thing the marker was guarding against.
  - `<!-- model:claude-haiku-4-5-20251001 -->` overrides `ClaudeApiSettings:Model` for just this skill. Use it for a narrow, cheap skill that doesn't need Opus-level reasoning — e.g. `skills/kubex-cluster-count/SKILL.md` calls one MCP tool and reports the array length, so it runs on Haiku instead of the fleet-wide default. Every other skill keeps using whatever `ClaudeApiSettings:Model` is configured to.
- Skills are loaded once at startup (`internal/skills`), from a `skills/` folder resolved as the sibling of `backend/` — matching how every workflow in `.github/workflows/` runs the backend (from `backend/`). Override the location with a top-level `SkillsDirectory` config key if you ever need to.

## Frontend

`frontend/index.html` is a single static page (no build step) — open it directly in a browser, or serve it with any static file server. It's just one command box that calls `POST /api/claude/command`, the same call a Teams bot makes — type whatever a Teams user would type after `@KubexAI` and send it. There are no input fields for the API base URL or the shared `X-Api-Key`; edit the `API_BASE_URL`/`API_KEY` constants at the top of the page's `<script>` instead, since every secret this service needs already lives server-side (`appsettings*.json` / GitHub secrets), not typed into the page.

## Scheduled message (GitHub Actions)

`.github/workflows/good-morning.yml` asks Claude a "good morning" question and posts the answer to your Teams webhook on a cron schedule (daily by default — edit the `cron:` line to change it), without needing any server or computer left running. Each run: checks out the repo, builds and starts the backend in the Actions runner, configures the webhook URL, calls `POST /api/claude/command` with the prompt "Good morning! Tell me something new today about the news." — Claude's answer is posted to Teams automatically by that endpoint — then tears everything down.

Add these as **repository secrets** (GitHub repo → Settings → Secrets and variables → Actions → New repository secret):

- `API_KEY` — the same shared secret as `ApiKeySettings:Key`
- `TEAMS_WEBHOOK_URL` — your Teams webhook URL
- `CLAUDE_API_KEY` — an Anthropic API key, same as `ClaudeApiSettings:ApiKey`

You can trigger it on demand from the repo's **Actions** tab (it has `workflow_dispatch` enabled) instead of waiting for the schedule, to test it right after setting up secrets.

### On-demand Claude command (GitHub Actions as a bot backend)

`.github/workflows/claude-command.yml` is a second workflow, triggered only via `workflow_dispatch` (no schedule), that takes a `command` input and sends it to `POST /api/claude/command` — same start-backend-and-call flow as the good-morning workflow, but with whatever text you pass in instead of a fixed prompt. It's a way to run the bot without keeping any server of your own running, at the cost of roughly a minute of latency per command (checkout + build + startup) instead of an instant reply from an always-on backend.

To trigger it from something like a Power Automate flow (instead of that flow calling your backend's `/api/claude/command` directly), call the GitHub API:

```
POST https://api.github.com/repos/Demqnd/KubexHealthCheck/actions/workflows/claude-command.yml/dispatches
Authorization: Bearer <a GitHub personal access token>
Accept: application/vnd.github+json
Content-Type: application/json

{
  "ref": "main",
  "inputs": {
    "command": "<the Teams message text>"
  }
}
```

Notes:

- The token needs `Actions: Read and write` permission on this repo — create a **fine-grained personal access token** (GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens) scoped to just this repository, rather than a classic token with broad `repo` scope.
- This call returns `202 Accepted` immediately — it does **not** wait for the workflow to finish or return Claude's answer. The answer still only reaches Teams the same way as before: the workflow calls `/api/claude/command`, which posts the response to your configured webhook itself.
- Each run does a full `go build` from scratch (no dependency/build caching configured), so expect ~30-60 seconds between sending a command and seeing a reply in Teams.
- Running this repeatedly is still free or near-free — GitHub Actions is unlimited on public repos, and private repos get 2,000 free minutes/month on the default plan, well beyond what casual bot usage would use.
