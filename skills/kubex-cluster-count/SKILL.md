<!-- model:claude-haiku-4-5-20251001 -->

# Kubex Cluster Count

## What this skill does

Given a client identifier (a Kubex MCP hostname like `sandboxuat-mcp.kubex.ai`, or a short client name), report only how many Kubernetes clusters are connected for that client. Nothing else — no status, freshness, or version-drift information, even if it's sitting right there in the tool's response. If the caller wants the full picture, that's `kubex-health-check`, not this.

## Two ways this skill runs

- **Via the KubexHealthCheck backend** — invoked as a Teams command like `@KubexAI https://sandboxuat-mcp.kubex.ai kubex-cluster-count`. The MCP server is already attached to this request with the exact URL from the command, and you'll be told so explicitly with a context line starting `[Context: this request already has a Kubex MCP server attached, for ...]`. Trust that line — skip connector resolution entirely and go straight to "Steps."
- **Interactively, in Claude Code / Claude Desktop / claude.ai** — there is no MCP server already attached; resolve the parameter to one of your connected Kubex connectors yourself (call a connector-list tool, match by hostname, tolerant of scheme/trailing-slash/case differences but not a looser guess). If there's no clean match, say so and name what is connected rather than guessing.

## Steps

1. Call the Kubex cluster-connections tool for the connector (e.g. `kubex-cluster-connections`).
2. Count the entries returned — that's clusters connected, not node or container counts inside them.
3. Reply with exactly one short sentence: the count, e.g. "14 clusters connected." Do not add status, freshness, or version details even though the tool response includes them — the caller only wants the number.

## Output style

One short sentence, plain text, no markdown formatting. Just the count, e.g. "14 clusters connected."
