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

    // Adapted from the "Kubex Health Check" Claude Code skill (see skills/kubex-health-check/SKILL.md)
    // for use over the raw Messages API: no connector-name resolution (the MCP URL is already given
    // explicitly) and no local file write (the answer is posted to Teams by this service instead).
    private const string KubexMcpSystemPrompt =
        "You are an SRE assistant with access to a connected Kubex MCP server's tools. When asked about " +
        "cluster health, call the Kubex cluster-connections tool to get per-cluster data: clusterName, " +
        "status, lastDataCollectionTime, forwarderVersion, prometheusVersion, kubernetesVersion, nodeCount, " +
        "containerCount. Each entry is one cluster connection. " +
        "Produce a short plain-text summary, in this order: " +
        "1) Cluster count - how many clusters are connected, e.g. \"14 clusters connected.\" " +
        "2) Status check - a cluster is healthy if its status is \"Ready\" or \"Collecting\"; anything else " +
        "needs action. If all healthy, say so in one line. If not, name the unhealthy cluster(s) and their " +
        "status, e.g. \"2 clusters need attention: foo-cluster (Error), bar-cluster (Disconnected).\" " +
        "3) Freshness check - compare each cluster's lastDataCollectionTime to now (default to US Eastern " +
        "time unless told otherwise) against a 24-hour window. If all fresh, say so and include the most " +
        "recent collection time in hours to one decimal place, e.g. \"All 14 clusters have collected data " +
        "in the past 24 hours (most recent: 9.1h ago).\" If any are stale, name them and how stale, e.g. " +
        "\"3 of 14 clusters haven't collected in over 24 hours: foo-cluster (last seen 31h ago).\" " +
        "4) Version drift - for forwarderVersion and prometheusVersion separately: if uniform across all " +
        "clusters, say so (\"all 14 clusters on forwarder v4.3.0\"); otherwise name only the oldest version " +
        "present and how many clusters run it (\"oldest forwarder version is v4.1.0, running on 2 of 14 " +
        "clusters\"). Don't list every version/cluster combination. " +
        "Keep the whole summary to one tight paragraph, plain text only (no markdown, no headers, no " +
        "bullets, no bold) - it will be posted directly as a Teams message.";

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
            if (string.IsNullOrWhiteSpace(instruction))
            {
                instruction = "Check the fleet's health.";
            }

            return CallClaudeAsync(
                settings.ApiKey, model, KubexMcpSystemPrompt, instruction, cancellationToken, mcpServerUrl);
        }

        // No MCP URL — see if the first word names an installed skill
        // (skills/<name>/SKILL.md, loaded by SkillRegistry). If so, that
        // file's contents become the system prompt and everything after the
        // skill word becomes the user's instruction to it. Otherwise, fall
        // back to a plain free-form question.
        var (skill, rest) = ResolveSkill(content);
        if (skill is not null)
        {
            var skillInput = BuildDateContext() + (string.IsNullOrWhiteSpace(rest) ? "Run this skill." : rest);
            return CallClaudeAsync(settings.ApiKey, model, skill.Instructions, skillInput, cancellationToken);
        }

        return CallClaudeAsync(settings.ApiKey, model, AskSystemPrompt, content, cancellationToken);
    }

    private (Skill? Skill, string Rest) ResolveSkill(string content)
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
                    ["mcp_server_name"] = McpServerName
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
