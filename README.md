# KubexHealthCheck

A standalone webhook routine service: configure a webhook URL and send it messages on demand. Originally built as an admin feature inside expenseDensify, extracted here as its own project. Tested against a Microsoft Teams "Workflows" incoming webhook, which requires messages wrapped as an Adaptive Card attachment — `WebhookMessageSender` builds that shape automatically.

## Backend

ASP.NET Core Web API (`backend/`), .NET 10. No database — the webhook URL is persisted to a small JSON file (`backend/data/webhook-routine.json`, gitignored) next to the project, so there's nothing to install or run besides the .NET SDK.

Setup:

```bash
cd backend
dotnet restore
```

Every endpoint requires a shared-secret API key, since this service is meant to be reachable over the public internet (e.g. by a Teams bot). Send it as an `X-Api-Key` header on every request; requests without it, or with the wrong value, get `401`.

Configure `appsettings.Development.json` (or environment variables):

- `ApiKeySettings:Key` — the shared secret. Generate one yourself (e.g. `openssl rand -hex 24`) and put the same value here, in the frontend's "API key" field, and in the `API_KEY` GitHub Actions secret. If this is left empty, the server responds `500` on every request rather than silently allowing unauthenticated access.
- `WebhookSettings:DefaultUrl` — optional. If set, this becomes the webhook URL used by `/api/webhook/send`, `/api/claude/command`, and the health check, with no `PUT /api/webhook` call needed first — useful so a fresh clone works immediately without a manual setup step. `PUT /api/webhook` still works as before and overrides this once called (the override is saved to `webhook-routine.json` and takes precedence from then on). Since this URL itself contains a secret signature, don't commit your real value — fill it in locally only, or set it via the `WebhookSettings__DefaultUrl` environment variable, same as `ApiKeySettings:Key`.
- `KubexApiSettings:BaseUrl` — your Kubex dashboard URL (e.g. `https://your-instance.kubex.ai`), used for the Kubex health check feature.
- `KubexApiSettings:Username` / `KubexApiSettings:Password` — credentials for an **API-enabled** Kubex user (see Kubex's `POST /authorize` docs). Required only for `POST /api/kubexhealthcheck/run`.
- `ClaudeApiSettings:ApiKey` — an Anthropic API key. Used by `POST /api/kubexhealthcheck/run` for the AI-written summary (falls back to a deterministic summary if unset or if the call fails) and by `POST /api/claude/command` for the bot-facing command endpoint.
- `ClaudeApiSettings:Model` — defaults to `claude-opus-5`.
- `KubexMcpSettings:AuthorizationToken` — optional. Lets a bot command connect Claude to a Kubex MCP server for that one request — see "MCP-connected commands" below.
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

### Word-dispatched skills

If a `command` has no MCP URL in it, the backend checks whether its first word names an installed skill — a folder under `skills/` containing a `SKILL.md`. If it matches (case- and punctuation-insensitive, and tolerant of a leading `@KubexAI` mention) **and isn't marked `dispatch:false`**, that file's contents become the system prompt for the request, and everything after the skill word becomes the instruction sent to Claude. If nothing matches, the command is treated as a plain free-form question, same as before.

**Adding a new skill is just adding a folder** — no code change, no config edit, no restart logic to wire up:

```
skills/
  onthisday/
    SKILL.md      # dispatched as "@KubexAI onthisday"
  kubex-health-check/
    SKILL.md      # dispatch:false — only usable via the MCP-URL path above, see that section
```

- The dispatch word is the folder name. `onthisday`, `OnThisDay`, and `onthisday?` (trailing punctuation from a Teams message) all resolve to the same skill.
- `SKILL.md`'s full contents are used as-is as the system prompt — write it the same way you'd write instructions for a Claude Code skill (see `skills/onthisday/SKILL.md` for the pattern: what it does, how to read the invocation, step-by-step rules, and the exact output style). `skills/kubex-health-check/SKILL.md` is a more advanced example: one file, written to work correctly in *two* different contexts (interactive Claude Code/Desktop, and this backend) by explicitly branching on which one applies at the one step where they differ.
- Every skill dispatch is automatically given the current date (`US Eastern`) as a short context line before the instruction, since Claude has no clock of its own — a skill that needs "today" (like `onthisday`) can just say so in its instructions and rely on that context being there.
- `<!-- dispatch:false -->` as the first line means: **not usable from this plain word-dispatch path**, because that logic needs something code-level this path can't provide — an MCP server actually attached to the request being the main case. It does *not* mean "never used" — see MCP-connected commands above, where the same skill is fully usable once a URL supplies the thing the marker was guarding against.
- Skills are loaded once at startup (`SkillRegistry` in `backend/src/Services/`), from a `skills/` folder resolved as the sibling of `backend/` — matching how every workflow in `.github/workflows/` runs the backend (`dotnet run` from `backend/`). Override the location with a top-level `SkillsDirectory` config key if you ever need to.

## Frontend

`frontend/index.html` is a single static page (no build step) — open it directly in a browser, or serve it with any static file server. Fill in the API base URL, then use it to save the webhook URL, send messages, run the Kubex health check, or ask Claude a one-off question (pasting your own Anthropic API key into that form).

## Scheduled message (GitHub Actions)

`.github/workflows/good-morning.yml` asks Claude a "good morning" question and posts the answer to your Teams webhook on a cron schedule (daily by default — edit the `cron:` line to change it), without needing any server or computer left running. Each run: checks out the repo, builds and starts the backend in the Actions runner, configures the webhook URL, calls `POST /api/claude/command` with the prompt "Good morning! Tell me something new today about the news." — Claude's answer is posted to Teams automatically by that endpoint — then tears everything down.

This intentionally does **not** call `/api/kubexhealthcheck/run` — that endpoint needs a fully working `KubexApiSettings` setup (Kubex base URL + API-enabled credentials), which isn't in place yet and was failing. Once Kubex auth is sorted out, swap the "Send good morning message" step to hit `/api/kubexhealthcheck/run` instead to resume real health checks.

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
- Each run does a full `dotnet build` from scratch (no dependency/build caching configured), so expect ~30-90 seconds between sending a command and seeing a reply in Teams.
- Running this repeatedly is still free or near-free — GitHub Actions is unlimited on public repos, and private repos get 2,000 free minutes/month on the default plan, well beyond what casual bot usage would use.
