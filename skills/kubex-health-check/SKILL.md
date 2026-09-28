# Kubex Health Check

## What this skill does

Given a client identifier (a Kubex MCP hostname like `sandboxuat-mcp.kubex.ai`, or a short client name), pull that client's Kubernetes cluster connection data from Kubex and produce a short, clean health summary covering:

1. **Cluster count** — how many clusters are under this connection.
2. **Connection status** — is every cluster in a healthy state, or does something need action?
3. **Data freshness** — has every cluster collected data in the last 24 hours?
4. **A per-cluster breakdown** — for every cluster: its real status, its collector (forwarder) version (flagged if it's not the newest version present), and its container count.

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

1. Call the Kubex cluster-connections tool for the connector (e.g. `kubex-cluster-connections`). This returns, per cluster: `clusterName`, `status`, `lastDataCollectionTime`, `forwarderVersion`, `prometheusVersion`, `kubernetesVersion`, `nodeCount`, `containerCount`. Note that each entry here is one cluster connection — that's the unit of counting and status/freshness checks below, not the individual node counts inside a cluster.
2. **Cluster count:** count the entries returned. This number is always reported, e.g. "14 clusters connected."
3. **Status check:** a cluster is healthy if its `status` is one of the good/active states — `Ready` or `Collecting` are both fine (both mean the pipeline is up; `Collecting` just tends to mean it's newer / still backfilling). Any other status is not one of those and needs action: call it out explicitly with the cluster name(s) and the status value, e.g. "2 clusters need attention: `foo-cluster` (Error), `bar-cluster` (Disconnected)." If every cluster is healthy, say so in one line rather than listing all of them.
4. **Freshness check (24-hour window):** compare each cluster's `lastDataCollectionTime` to the current time. Default to US Eastern time (EST/EDT) for "now" unless the user has told you a different timezone to use, or a current-date context was already given to you (use that instead of guessing).
   - If every cluster collected within the last 24 hours, say so in one line and include how recent the most current cluster's collection is, e.g. "All N clusters have collected data in the past 24 hours (most recent: 9.1h ago)." Compute that "most recent" figure as the smallest hours-since-collection value across all clusters, to one decimal place.
   - If any cluster hasn't, report how many and which ones need action, with how stale each is, e.g. "3 of 14 clusters haven't collected in over 24 hours and need action: `foo-cluster` (last seen 31h ago), ..."
   - If the user asks for a different format (e.g. just hours, or hours+minutes) or a different staleness window, use that instead.
5. **Per-cluster breakdown:** list every cluster returned, one per line, with its real `status` value, its `forwarderVersion`, and its `containerCount`. Determine the newest `forwarderVersion` present among this run's clusters (the highest version number in the batch), and flag any cluster not on that version as `(outdated)`. Format each line consistently, e.g.:
   `foo-cluster: Ready, collector v4.7.3, 6000 containers`
   `lilly-kubed-prd: Collecting, collector v4.2.6 (outdated), 953 containers`
   This is a full listing, not just the clusters needing attention — every cluster gets a line. `prometheusVersion`/`kubernetesVersion` drift can still be mentioned as a brief aside (e.g. "all on Kubernetes v1.28") if it's uniform or notably not, but don't repeat a full breakdown of those two on top of the per-cluster lines above.
6. **Summary:** lead with the cluster count, the status verdict, and the 24-hour freshness verdict as a short headline, then the full per-cluster breakdown from step 5.
7. **Deliver the result — this step depends on which context you're running in:**
   - **Interactively (Claude Code / Claude Desktop), with local file access:** write the result as JSON to `kubex-health-latest.json` inside `C:\Users\conno\Claude Cowork` (connect that folder via `mcp__cowork__request_cowork_directory` first if it isn't already mounted). This is picked up by a separate local script that posts a Teams/Power Automate notification — your job stops at writing an accurate file. Use this exact shape:

     ```json
     {
       "timestamp": "<ISO-8601, US Eastern>",
       "clusterCount": 15,
       "statusHealthy": true,
       "statusIssues": [{"clusterName": "foo-cluster", "status": "Error"}],
       "freshnessHealthy": true,
       "freshestHoursAgo": 9.1,
       "staleClusters": [{"clusterName": "foo-cluster", "hoursSinceCollection": 31}],
       "forwarderOldestVersion": "v4.3.0",
       "forwarderOldestCount": 15,
       "prometheusOldestVersion": "2.46.0",
       "prometheusOldestCount": 1,
       "kubernetesOldestVersion": "1.28",
       "kubernetesOldestCount": 3,
       "summary": "<the one-paragraph plain-text summary shown in chat>"
     }
     ```

     `statusIssues` and `staleClusters` are empty arrays when everything's healthy/fresh.
   - **Via the KubexHealthCheck backend:** there is no local filesystem to write to, and no separate relay script — just answer in plain text (see "Output style" below). The backend itself posts your response directly to the configured Teams webhook; nothing else needs to happen after you answer.

## Output style

Plain text — no markdown formatting (no headers, bullets, or bold), since this is posted directly as a Teams message. Structure:

1. A short headline line: cluster count → status (all healthy, or which need action) → freshness (all current, or which need action).
2. Then one line per cluster (the per-cluster breakdown from step 5), each on its own line in the `<clusterName>: <status>, collector vX.Y.Z<optional " (outdated)">, <N> containers` format.

No mention of which connector was matched unless there was an ambiguity worth flagging. If you wrote a local result file (interactive context only), mention it briefly, e.g. "(saved to kubex-health-latest.json)" — don't dwell on it.

## Example invocations

- Interactive: `/kubexHealthCheck sandboxuat-mcp.kubex.ai` — resolve `sandboxuat-mcp.kubex.ai` to its connected Kubex connector (silently), call its cluster-connections tool, return the summary, write `kubex-health-latest.json` to `C:\Users\conno\Claude Cowork`.
- Backend: `run health check` (typed into the frontend, no URL or client name needed) — the backend runs this once per customer in `customers.csv`, already having called the cluster-connections tool for each one; skip straight to "Steps" using that data, and answer in plain text for each — the backend combines every customer's answer and posts the combined report to Teams for you.
