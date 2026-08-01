# Kubex Health Check

## What this skill does

Given a client identifier (a Kubex MCP hostname like `sandboxuat-mcp.kubex.ai`, or a short client name), pull that client's Kubernetes cluster connection data from Kubex and produce a short, clean health summary covering:

1. **Cluster count** — how many clusters are under this connection.
2. **Connection status** — is every cluster in a healthy state, or does something need action?
3. **Data freshness** — has every cluster collected data in the last 24 hours?
4. **Version drift** — is the fleet on one forwarder/Prometheus version, or is there an oldest version lagging behind?

The parameter is designed to be swappable — the same steps below should work whether the client this run is for is `sandboxuat-mcp.kubex.ai`, some other Kubex MCP host, or a short client name, as long as that client is already connected.

## Reading the parameter

When invoked as `/kubexHealthCheck <parameter>`, treat everything after the command as the **client identifier**.

**Important constraint:** this parameter selects which *already-connected* Kubex MCP connector to query — it does not create a new connection on the fly. Each client's Kubex instance must already be added and authorized as a Claude connector (via Settings > Connectors, or Organization settings > Connectors for Team/Enterprise plans) before this skill can query it.

To resolve the parameter to an actual connector, don't just assume — look it up:

1. Call the connector registry/list tool (e.g. `list_connectors`, filtered to `["kubex"]`) to get the real names and URLs of every connected Kubex connector.
2. Match the parameter against that list. Be tolerant of superficial differences — scheme (`https://`), trailing slashes, and case shouldn't block a match — but a real match should still be a genuine one-to-one correspondence, not a "close enough" guess. If the parameter differs from a connected connector's URL by more than that (extra/missing words, a different subdomain, `uat` vs. no `uat`, etc.), don't silently substitute the closest one.
3. If there's exactly one clean match, use that connector's tools for this run. **This resolution step is internal** — don't mention the connector's name/URL or that it was matched in the final output. It's only worth surfacing when something's actually wrong (see below).
4. If there's no match, or more than one plausible match, say so plainly: name the connector(s) that *are* connected, note that the requested client doesn't appear to be one of them, and point to Settings > Connectors to add it. Do not guess or fabricate data for an unconnected or ambiguous client.
5. If only one Kubex connector is available and no parameter is given, ask which client this run is for rather than assuming.

## Steps

1. Call the Kubex cluster-connections tool for the matched connector (e.g. `kubex-cluster-connections`). This returns, per cluster: `clusterName`, `status`, `lastDataCollectionTime`, `forwarderVersion`, `prometheusVersion`, `kubernetesVersion`, `nodeCount`, `containerCount`. Note that each entry here is one cluster connection — that's the unit of counting and status/freshness checks below, not the individual node counts inside a cluster.
2. **Cluster count:** count the entries returned. This number is always reported, e.g. "14 clusters connected."
3. **Status check:** a cluster is healthy if its `status` is one of the good/active states — `Ready` or `Collecting` are both fine (both mean the pipeline is up; `Collecting` just tends to mean it's newer / still backfilling). Any other status is not one of those and needs action: call it out explicitly with the cluster name(s) and the status value, e.g. "2 clusters need attention: `foo-cluster` (Error), `bar-cluster` (Disconnected)." If every cluster is healthy, say so in one line rather than listing all of them.
4. **Freshness check (24-hour window):** compare each cluster's `lastDataCollectionTime` to the current time. Default to US Eastern time (EST/EDT) for "now" unless the user has told you a different timezone to use.
   - If every cluster collected within the last 24 hours, say so in one line and include how recent the most current cluster's collection is, e.g. "All N clusters have collected data in the past 24 hours (most recent: 9.1h ago)." Compute that "most recent" figure as the smallest hours-since-collection value across all clusters, to one decimal place.
   - If any cluster hasn't, report how many and which ones need action, with how stale each is, e.g. "3 of 14 clusters haven't collected in over 24 hours and need action: `foo-cluster` (last seen 31h ago), ..."
   - If the user asks for a different format (e.g. just hours, or hours+minutes) or a different staleness window, use that instead.
5. **Version drift check:** keep this to the oldest-version headline, not a full version-by-version breakdown.
   - For `forwarderVersion`: if every cluster is on the same version, say "all N clusters on forwarder vX." If not, name the oldest version present and how many clusters are on it, e.g. "oldest forwarder version is vX, running on M of N clusters."
   - For `prometheusVersion`: same pattern — "all N clusters on Prometheus vX" if uniform, otherwise "oldest Prometheus version is vX, running on M of N clusters."
   - Don't list every version/cluster combination — just the oldest one and its count. If the user has told Claude what the current/latest version is, you can additionally say how far behind that oldest version is. Don't assume what "latest" is if it hasn't been provided.
6. **Summary:** always lead with the cluster count, the status verdict, and the 24-hour freshness verdict — these three are the headline and shouldn't be held back for a "detailed" ask. Version drift follows in the oldest-version form described above unless the user wants the full per-cluster table.
7. **Write the result file (for the local webhook relay):** after producing the summary, write it as JSON to `kubex-health-latest.json` inside the folder `C:\Users\conno\Claude Cowork`. This is the fixed, standing location for this file — always write here, every run, without asking the user. If that folder isn't already mounted/connected in the current session, call `mcp__cowork__request_cowork_directory` with `path` set to `C:\Users\conno\Claude Cowork` to connect it automatically (do not fall back to asking the user or skipping the step unless that request itself fails or is declined — only then tell the user the file couldn't be written and why). Overwrite the file each run. Use this exact shape:

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
     "summary": "<the one-paragraph plain-text summary shown in chat>"
   }
   ```

   `statusIssues` and `staleClusters` are empty arrays when everything's healthy/fresh. This file is picked up by a separate local script (outside this skill, running on the user's own machine) that posts a Teams/Power Automate notification — this skill's job stops at writing an accurate file, not at sending anything itself. Claude's sandbox can't reach arbitrary outbound webhooks (only an allowlisted set of domains), which is why the actual notification send happens locally instead of from within this skill.

## Output style

Lead with: cluster count → status (all healthy, or which need action) → freshness (all current, or which need action) → version drift (oldest version + count, or "all on this version"). No mention of which connector was matched unless there was an ambiguity worth flagging. Don't repeat the full per-cluster table beyond what's needed to name the clusters that need action. Mention the result file was written only briefly, e.g. "(saved to kubex-health-latest.json)" — don't dwell on it.

## Example invocation

`/kubexHealthCheck sandboxuat-mcp.kubex.ai`

Expected behavior: resolve `sandboxuat-mcp.kubex.ai` to its connected Kubex connector (silently), call its cluster-connections tool, return the cluster count / status / freshness / version-drift summary, write the same data as `kubex-health-latest.json` to `C:\Users\conno\Claude Cowork` for the local webhook relay script to pick up.

---

## Note: this file is a backup, not a live dependency

This is your personal Claude Code / Claude Desktop skill, copied here for version control. It is **not** read by the KubexHealthCheck backend or the raw Claude API — Skills are a Claude Code/Claude.ai feature (loaded by that harness based on the trigger description), not something `POST /v1/messages` understands on its own.

The backend's `/api/claude/command` endpoint (see `backend/src/Services/ClaudeSummaryService.cs`) implements the *equivalent* analytical logic directly as a system prompt (`KubexMcpSystemPrompt`), adapted for the API context:

- No connector resolution (steps in "Reading the parameter" above) — the MCP server URL is already given explicitly in the command text, so there's no ambiguous client name to match against a connector list.
- No local file write (step 7 above) — the backend has no access to your PC's filesystem. Instead, the response is posted straight to your Teams webhook by the backend itself (see `POST /api/claude/command` in the main README).

Keep both in sync by hand if you change the analytical rules (status values considered healthy, the 24-hour freshness window, the version-drift format) — update this file *and* `KubexMcpSystemPrompt` in `ClaudeSummaryService.cs`.
