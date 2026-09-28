# Kubex Health Check

## What this skill does

Given a client identifier (a Kubex MCP hostname like `sandboxuat-mcp.kubex.ai`, or a short client name), pull that client's Kubernetes cluster connection data from Kubex and produce a simple table, one row per cluster, so a dev can glance at it and immediately tell whether everything's working — no reading required, just a quick scan. Each row has: cluster name, status, collector (forwarder) version (flagged if it's not the newest version present), and container count. Anything that needs a closer look (a bad status, an outdated version) is visible right there in the row; a dev who spots one goes and investigates that specific cluster themselves.

The parameter is designed to be swappable — the same steps below should work whether the client this run is for is `sandboxuat-mcp.kubex.ai`, `fedex-mcp.kubex.ai`, some other Kubex MCP host, or a short client name, as long as that client is already connected.

## Two ways this skill runs

This file is used in two different contexts, and the first step differs between them:

- **Interactively, in Claude Code / Claude Desktop / claude.ai** — invoked as `/kubexHealthCheck <parameter>`. There is no MCP server already attached to the request; you have to resolve the parameter to one of your connected Kubex connectors yourself (see "Resolving a bare parameter" below), then call the tool yourself.
- **Via the KubexHealthCheck backend** — invoked with a command containing "health check" (e.g. typing `run health check` into the frontend). The backend runs this skill once per customer listed in `customers.csv`, and for each one it **already calls the `kubex-cluster-connections` tool itself** before ever calling you, then hands you the real result directly as context — something like `[Context: here is the real, current output of the kubex-cluster-connections tool for <customer> — that's the client this run is for. Do not ask which client to use, and don't try to call any tools yourself — just use this data:] <json>`. Trust that line — **skip connector resolution and skip calling any tool** whenever it's present; go straight to "Steps" below using the data already given to you. (If you don't see a line like that, you're in the interactive case above — go resolve the parameter and call the tool yourself instead.)

### Resolving a bare parameter (interactive use only — skip this if a URL was already given)

**Important constraint:** the parameter selects which *already-connected* Kubex MCP connector to query — it does not create a new connection on the fly. Each client's Kubex instance must already be added and authorized as a Claude connector (via Settings > Connectors, or Organization settings > Connectors for Team/Enterprise plans) before this skill can query it.

To resolve the parameter to an actual connector, don't just assume — look it up:

1. Call the connector registry/list tool (e.g. `list_connectors`, filtered to `["kubex"]`) to get the real names and URLs of every connected Kubex connector.
2. Match the parameter against that list. Be tolerant of superficial differences — scheme (`https://`), trailing slashes, and case shouldn't block a match — but a real match should still be a genuine one-to-one correspondence, not a "close enough" guess. If the parameter differs from a connected connector's URL by more than that (extra/missing words, a different subdomain, `uat` vs. no `uat`, etc.), don't silently substitute the closest one.
3. If there's exactly one clean match, use that connector's tools for this run. **This resolution step is internal** — don't mention the connector's name/URL or that it was matched in the final output. It's only worth surfacing when something's actually wrong (see below).
4. If there's no match, or more than one plausible match, say so plainly: name the connector(s) that *are* connected, note that the requested client doesn't appear to be one of them, and point to Settings > Connectors to add it. Do not guess or fabricate data for an unconnected or ambiguous client.
5. If only one Kubex connector is available and no parameter is given, ask which client this run is for rather than assuming.

## Steps

1. Call the Kubex cluster-connections tool for the connector (e.g. `kubex-cluster-connections`). This returns, per cluster: `clusterName`, `status`, `lastDataCollectionTime`, `forwarderVersion`, `prometheusVersion`, `kubernetesVersion`, `nodeCount`, `containerCount`. Each entry here is one cluster connection — that's the row unit below, not the individual node counts inside a cluster.
2. Determine the newest `forwarderVersion` present among this run's clusters (the highest version number in the batch) — that's the reference point for flagging any other cluster as outdated.
3. Build one row per cluster returned — every cluster gets a row, not just ones with a problem — with exactly these four fields: cluster name, its real `status` value (don't invent or relabel it — report `Ready`, `Collecting`, `Error`, `Disconnected`, etc. exactly as returned), its `forwarderVersion` (append `(outdated)` if it isn't the newest version found in step 2), and its `containerCount`.
4. **Deliver the result — this step depends on which context you're running in:**
   - **Interactively (Claude Code / Claude Desktop), with local file access:** write the result as JSON to `kubex-health-latest.json` inside `C:\Users\conno\Claude Cowork` (connect that folder via `mcp__cowork__request_cowork_directory` first if it isn't already mounted). This is picked up by a separate local script that posts a Teams/Power Automate notification — your job stops at writing an accurate file. Use this exact shape:

     ```json
     {
       "timestamp": "<ISO-8601, US Eastern>",
       "clusterCount": 15,
       "newestForwarderVersion": "v4.7.3",
       "clusters": [
         {"clusterName": "foo-cluster", "status": "Ready", "forwarderVersion": "v4.7.3", "outdated": false, "containerCount": 6000},
         {"clusterName": "lilly-kubed-prd", "status": "Collecting", "forwarderVersion": "v4.2.6", "outdated": true, "containerCount": 953}
       ],
       "summary": "<the plain-text table shown in chat>"
     }
     ```

     One entry in `clusters` per cluster returned, in the same order as the tool result.
   - **Via the KubexHealthCheck backend:** there is no local filesystem to write to, and no separate relay script — just answer in plain text (see "Output style" below). The backend itself posts your response directly to the configured Teams webhook; nothing else needs to happen after you answer.

## Output style

Just the table — plain text, no markdown formatting (no headers, bullets, or bold), since this is posted directly as a Teams message. No summary paragraph, no status/freshness verdict sentence, no preamble — a dev scanning it should see the table immediately. Format:

```
Cluster | Status | Version | Containers
foo-cluster | Ready | v4.7.3 | 6000
lilly-kubed-prd | Collecting | v4.2.6 (outdated) | 953
```

One header row, then one row per cluster from step 3, `|`-separated, same column order every time. No mention of which connector was matched unless there was an ambiguity worth flagging — in that case, say so as a separate line before the table. If you wrote a local result file (interactive context only), add one line after the table, e.g. "(saved to kubex-health-latest.json)."

## Example invocations

- Interactive: `/kubexHealthCheck sandboxuat-mcp.kubex.ai` — resolve `sandboxuat-mcp.kubex.ai` to its connected Kubex connector (silently), call its cluster-connections tool, return the summary, write `kubex-health-latest.json` to `C:\Users\conno\Claude Cowork`.
- Backend: `run health check` (typed into the frontend, no URL or client name needed) — the backend runs this once per customer in `customers.csv`, already having called the cluster-connections tool for each one; skip straight to "Steps" using that data, and answer in plain text for each — the backend combines every customer's answer and posts the combined report to Teams for you.
