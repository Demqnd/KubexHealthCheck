using System.Text;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using KubexHealthCheck.Config;
using Microsoft.Extensions.Options;

namespace KubexHealthCheck.Services;

public class ClaudeSummaryService(
    IHttpClientFactory httpClientFactory,
    IOptions<ClaudeApiSettings> claudeOptions,
    IOptions<KubexMcpSettings> mcpOptions,
    ISkillRegistry skillRegistry)
    : IClaudeSummaryService
{
    private const string ApiUrl = "https://api.anthropic.com/v1/messages";
    private const string AnthropicVersion = "2023-06-01";
    private const string McpBetaHeader = "mcp-client-2025-11-20";
    private const string DefaultModel = "claude-opus-5";
    private const string McpServerName = "kubex-mcp";

    // The only Kubex MCP tool this service actually calls right now. Used to
    // allowlist the mcp_toolset below — see the comment on "tools" in
    // CallClaudeAsync for why that matters.
    private const string RequiredMcpToolName = "kubex-cluster-connections";

    private static readonly Regex McpUrlPattern = new(@"https?://\S+", RegexOptions.Compiled);

    // Strips a leading "@KubexAI" (or any other @-mention) so the word right
    // after it is what gets checked against the skill registry — Teams
    // message text arrives with the mention still in it.
    private static readonly Regex LeadingMentionPattern = new(@"^@\S+\s*", RegexOptions.Compiled);

    private const string HealthCheckSystemPrompt =
        "You are an SRE assistant. You are given raw Kubernetes cluster health JSON collected by Kubex. " +
        "Write a short, plain-text summary suitable for posting in a Teams message. Call out any cluster " +
        "whose data collection is stale (no data in the last 24 hours), any Kubernetes version drift across " +
        "clusters, and any node or container counts that look concerning. Keep it under 200 words. Do not " +
        "use markdown formatting (no headers, bullets, or bold).";

    private const string AskSystemPrompt =
        "You are a helpful assistant. Answer the user's question clearly and concisely, in plain text " +
        "suitable for posting in a Teams message. Do not use markdown formatting (no headers, bullets, or bold).";

    // Last-resort fallback only: used if an MCP command can't find ANY loaded
    // skill to run — not even skills/kubex-health-check — which normally only
    // happens if the skills/ folder is missing or misconfigured (see
    // SkillsDirectory in SkillRegistry). In normal operation, the actual
    // skills/kubex-health-check/SKILL.md content is what runs; this constant
    // is not that file and will drift from it — it's a safety net, not a copy
    // to keep in sync.
    private const string FallbackKubexMcpSystemPrompt =
        "You are an SRE assistant with access to a connected Kubex MCP server's tools. Call the Kubex " +
        "cluster-connections tool to get per-cluster health data, and produce a short plain-text summary " +
        "covering cluster count, status, 24-hour data freshness, and forwarder/Prometheus version drift. " +
        "Keep it to one tight paragraph, no markdown formatting - it will be posted directly as a Teams message.";

    public Task<string> SummarizeHealthCheckAsync(string clusterDataJson, CancellationToken cancellationToken = default)
    {
        var settings = claudeOptions.Value;
        if (string.IsNullOrWhiteSpace(settings.ApiKey))
        {
            throw new InvalidOperationException("ClaudeApiSettings:ApiKey is not configured.");
        }

        var model = string.IsNullOrWhiteSpace(settings.Model) ? DefaultModel : settings.Model;
        return CallClaudeAsync(settings.ApiKey, model, HealthCheckSystemPrompt, clusterDataJson, cancellationToken);
    }

    public Task<string> AskAsync(string apiKey, string question, CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrWhiteSpace(apiKey))
        {
            throw new InvalidOperationException("An Anthropic API key is required.");
        }

        var configuredModel = claudeOptions.Value.Model;
        var model = string.IsNullOrWhiteSpace(configuredModel) ? DefaultModel : configuredModel;
        return CallClaudeAsync(apiKey, model, AskSystemPrompt, question, cancellationToken);
    }

    public Task<string> RunCommandAsync(string command, CancellationToken cancellationToken = default)
    {
        var settings = claudeOptions.Value;
        if (string.IsNullOrWhiteSpace(settings.ApiKey))
        {
            throw new InvalidOperationException("ClaudeApiSettings:ApiKey is not configured.");
        }

        var model = string.IsNullOrWhiteSpace(settings.Model) ? DefaultModel : settings.Model;

        // Drop a leading "@KubexAI" (or any @-mention) before anything below
        // looks at the command's first word — Teams delivers the mention as
        // literal text, and it would otherwise shadow a skill word.
        var content = LeadingMentionPattern.Replace(command.Trim(), string.Empty).Trim();
        if (string.IsNullOrWhiteSpace(content))
        {
            content = command.Trim();
        }

        var urlMatch = McpUrlPattern.Match(content);
        if (urlMatch.Success)
        {
            var mcpServerUrl = urlMatch.Value;
            var instruction = content.Remove(urlMatch.Index, urlMatch.Length).Trim();

            // Does the word right after the URL name a loaded skill (e.g.
            // "kubex-health-check", "fedex-cost-report")? This check ignores
            // GenericallyDispatchable on purpose: an MCP server is already
            // attached below, so a skill marked dispatch:false for the
            // no-MCP path is exactly usable here.
            var (mcpSkill, mcpRest) = ResolveSkill(instruction);
            if (mcpSkill is not null)
            {
                var mcpSkillInput = BuildDateContext() + BuildMcpContext(mcpServerUrl)
                    + (string.IsNullOrWhiteSpace(mcpRest) ? "Run this skill." : mcpRest);
                return CallClaudeAsync(
                    settings.ApiKey, model, mcpSkill.Instructions, mcpSkillInput, cancellationToken, mcpServerUrl);
            }

            // No recognized skill word after the URL (e.g. "@KubexAI <url>
            // check cluster status") — default to skills/kubex-health-check,
            // the same behavior this had before skill words existed here.
            var defaultSkill = skillRegistry.Find("kubex-health-check");
            var defaultInstruction = BuildMcpContext(mcpServerUrl)
                + (string.IsNullOrWhiteSpace(instruction) ? "Check the fleet's health." : instruction);
            if (defaultSkill is not null)
            {
                return CallClaudeAsync(
                    settings.ApiKey, model, BuildDateContext() + defaultSkill.Instructions, defaultInstruction,
                    cancellationToken, mcpServerUrl);
            }

            // skills/kubex-health-check/SKILL.md itself is missing or
            // unreadable (misconfigured SkillsDirectory, bad deploy, etc.) —
            // don't fail the request outright, answer with the fallback.
            return CallClaudeAsync(
                settings.ApiKey, model, FallbackKubexMcpSystemPrompt, defaultInstruction, cancellationToken, mcpServerUrl);
        }

        // No MCP URL — see if the first word names an installed skill
        // (skills/<name>/SKILL.md, loaded by SkillRegistry). Skills marked
        // dispatch:false are excluded here (GenericallyDispatchable == false)
        // since this path has no way to attach an MCP server for them.
        // Otherwise fall back to a plain free-form question.
        var (skill, rest) = ResolveSkill(content);
        if (skill is { GenericallyDispatchable: true })
        {
            var skillInput = BuildDateContext() + (string.IsNullOrWhiteSpace(rest) ? "Run this skill." : rest);
            return CallClaudeAsync(settings.ApiKey, model, skill.Instructions, skillInput, cancellationToken);
        }

        return CallClaudeAsync(settings.ApiKey, model, AskSystemPrompt, content, cancellationToken);
    }

    private (Skill? Skill, string Remainder) ResolveSkill(string content)
    {
        var spaceIndex = content.IndexOf(' ');
        var firstWord = spaceIndex < 0 ? content : content[..spaceIndex];
        var rest = spaceIndex < 0 ? string.Empty : content[(spaceIndex + 1)..].Trim();
        return (skillRegistry.Find(firstWord), rest);
    }

    // Claude has no clock of its own — skills that need "today" (like
    // onthisday) get it supplied here rather than guessing from training data.
    private static string BuildDateContext()
    {
        DateTime now;
        try
        {
            var eastern = TimeZoneInfo.FindSystemTimeZoneById("America/New_York");
            now = TimeZoneInfo.ConvertTimeFromUtc(DateTime.UtcNow, eastern);
        }
        catch (TimeZoneNotFoundException)
        {
            now = DateTime.UtcNow;
        }
        catch (InvalidTimeZoneException)
        {
            now = DateTime.UtcNow;
        }

        return $"[Context: today's date is {now:yyyy-MM-dd} ({now:dddd}), US Eastern.]\n\n";
    }

    // The MCP server URL never reaches Claude any other way — it's stripped
    // out of the command text and only ever appears in the request's
    // mcp_servers config, which Claude's own context doesn't surface as
    // readable text. Without this line, "no client identifier was mentioned"
    // and "a client was already specified via URL" are indistinguishable to
    // Claude, and a skill written to ask when nothing was specified (see
    // skills/kubex-health-check/SKILL.md) has no way to tell them apart —
    // it'll default to asking, every time, even though a URL was given.
    private static string BuildMcpContext(string mcpServerUrl) =>
        $"[Context: this request already has a Kubex MCP server attached, for {mcpServerUrl} — " +
        "that's the client this run is for. Do not ask which client to use, and skip any " +
        "connector-list/resolution step — just use the MCP tools already available to you.]\n\n";

    private async Task<string> CallClaudeAsync(
        string apiKey,
        string model,
        string systemPrompt,
        string userContent,
        CancellationToken cancellationToken,
        string? mcpServerUrl = null)
    {
        var requestBody = new JsonObject
        {
            ["model"] = model,
            ["max_tokens"] = string.IsNullOrWhiteSpace(mcpServerUrl) ? 1024 : 4096,
            ["thinking"] = new JsonObject { ["type"] = "disabled" },
            ["output_config"] = new JsonObject { ["effort"] = "low" },
            ["system"] = systemPrompt,
            ["messages"] = new JsonArray
            {
                new JsonObject
                {
                    ["role"] = "user",
                    ["content"] = userContent
                }
            }
        };

        if (!string.IsNullOrWhiteSpace(mcpServerUrl))
        {
            requestBody["mcp_servers"] = new JsonArray
            {
                new JsonObject
                {
                    ["type"] = "url",
                    ["url"] = mcpServerUrl,
                    ["name"] = McpServerName,
                    ["authorization_token"] = mcpOptions.Value.AuthorizationToken
                }
            };
            requestBody["tools"] = new JsonArray
            {
                new JsonObject
                {
                    ["type"] = "mcp_toolset",
                    ["mcp_server_name"] = McpServerName,
                    // Allowlist: without this, Anthropic loads the MCP server's
                    // ENTIRE tool catalog into every request (Kubex's server
                    // exposes ~28 tools with verbose descriptions - tens of
                    // thousands of input tokens billed on every call, even
                    // though this skill only ever calls one of them). Add a
                    // name here if a future skill needs a different Kubex tool.
                    ["default_config"] = new JsonObject { ["enabled"] = false },
                    ["configs"] = new JsonObject
                    {
                        [RequiredMcpToolName] = new JsonObject { ["enabled"] = true }
                    }
                }
            };
        }

        var client = httpClientFactory.CreateClient("ClaudeApi");
        using var request = new HttpRequestMessage(HttpMethod.Post, ApiUrl)
        {
            Content = new StringContent(requestBody.ToJsonString(), Encoding.UTF8, "application/json")
        };
        request.Headers.Add("x-api-key", apiKey);
        request.Headers.Add("anthropic-version", AnthropicVersion);
        if (!string.IsNullOrWhiteSpace(mcpServerUrl))
        {
            request.Headers.Add("anthropic-beta", McpBetaHeader);
        }

        using var response = await client.SendAsync(request, cancellationToken);
        var responseJson = await response.Content.ReadAsStringAsync(cancellationToken);

        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException(
                $"Claude API call failed with status {(int)response.StatusCode}: {responseJson}");
        }

        var node = JsonNode.Parse(responseJson);
        var stopReason = node?["stop_reason"]?.GetValue<string>();
        if (stopReason == "refusal")
        {
            throw new InvalidOperationException("Claude declined to respond to this request.");
        }

        // Take the LAST text block, not the first: when Claude uses an MCP tool,
        // the content array can contain preamble text before the tool call and
        // the real answer after the tool result.
        var textBlock = (node?["content"] as JsonArray)?
            .LastOrDefault(block => block?["type"]?.GetValue<string>() == "text");

        var text = textBlock?["text"]?.GetValue<string>();
        if (string.IsNullOrWhiteSpace(text))
        {
            throw new InvalidOperationException("Claude API response did not contain a text block.");
        }

        return text;
    }
}
