using System.Text;
using System.Text.Json.Nodes;
using KubexHealthCheck.Config;
using Microsoft.Extensions.Options;

namespace KubexHealthCheck.Services;

public class ClaudeSummaryService(IHttpClientFactory httpClientFactory, IOptions<ClaudeApiSettings> claudeOptions)
    : IClaudeSummaryService
{
    private const string ApiUrl = "https://api.anthropic.com/v1/messages";
    private const string AnthropicVersion = "2023-06-01";
    private const string DefaultModel = "claude-opus-5";

    private const string HealthCheckSystemPrompt =
        "You are an SRE assistant. You are given raw Kubernetes cluster health JSON collected by Kubex. " +
        "Write a short, plain-text summary suitable for posting in a Teams message. Call out any cluster " +
        "whose data collection is stale (no data in the last 24 hours), any Kubernetes version drift across " +
        "clusters, and any node or container counts that look concerning. Keep it under 200 words. Do not " +
        "use markdown formatting (no headers, bullets, or bold).";

    private const string AskSystemPrompt =
        "You are a helpful assistant. Answer the user's question clearly and concisely, in plain text " +
        "suitable for posting in a Teams message. Do not use markdown formatting (no headers, bullets, or bold).";

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
        return CallClaudeAsync(settings.ApiKey, model, AskSystemPrompt, command, cancellationToken);
    }

    private async Task<string> CallClaudeAsync(
        string apiKey,
        string model,
        string systemPrompt,
        string userContent,
        CancellationToken cancellationToken)
    {
        var requestBody = new JsonObject
        {
            ["model"] = model,
            ["max_tokens"] = 1024,
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

        var client = httpClientFactory.CreateClient("ClaudeApi");
        using var request = new HttpRequestMessage(HttpMethod.Post, ApiUrl)
        {
            Content = new StringContent(requestBody.ToJsonString(), Encoding.UTF8, "application/json")
        };
        request.Headers.Add("x-api-key", apiKey);
        request.Headers.Add("anthropic-version", AnthropicVersion);

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

        var textBlock = (node?["content"] as JsonArray)?
            .FirstOrDefault(block => block?["type"]?.GetValue<string>() == "text");

        var text = textBlock?["text"]?.GetValue<string>();
        if (string.IsNullOrWhiteSpace(text))
        {
            throw new InvalidOperationException("Claude API response did not contain a text block.");
        }

        return text;
    }
}
