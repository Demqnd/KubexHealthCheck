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

    private const string SystemPrompt =
        "You are an SRE assistant. You are given raw Kubernetes cluster health JSON collected by Kubex. " +
        "Write a short, plain-text summary suitable for posting in a Teams message. Call out any cluster " +
        "whose data collection is stale (no data in the last 24 hours), any Kubernetes version drift across " +
        "clusters, and any node or container counts that look concerning. Keep it under 200 words. Do not " +
        "use markdown formatting (no headers, bullets, or bold).";

    public async Task<string> SummarizeHealthCheckAsync(string clusterDataJson, CancellationToken cancellationToken = default)
    {
        var settings = claudeOptions.Value;
        if (string.IsNullOrWhiteSpace(settings.ApiKey))
        {
            throw new InvalidOperationException("ClaudeApiSettings:ApiKey is not configured.");
        }

        var requestBody = new JsonObject
        {
            ["model"] = settings.Model,
            ["max_tokens"] = 1024,
            ["thinking"] = new JsonObject { ["type"] = "disabled" },
            ["output_config"] = new JsonObject { ["effort"] = "low" },
            ["system"] = SystemPrompt,
            ["messages"] = new JsonArray
            {
                new JsonObject
                {
                    ["role"] = "user",
                    ["content"] = clusterDataJson
                }
            }
        };

        var client = httpClientFactory.CreateClient("ClaudeApi");
        using var request = new HttpRequestMessage(HttpMethod.Post, ApiUrl)
        {
            Content = new StringContent(requestBody.ToJsonString(), Encoding.UTF8, "application/json")
        };
        request.Headers.Add("x-api-key", settings.ApiKey);
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
            throw new InvalidOperationException("Claude declined to summarize this data.");
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
