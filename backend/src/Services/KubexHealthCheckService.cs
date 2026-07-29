using System.Net.Http.Headers;
using System.Text;
using System.Text.Json.Nodes;
using KubexHealthCheck.Config;
using Microsoft.Extensions.Options;

namespace KubexHealthCheck.Services;

public class KubexHealthCheckService(IHttpClientFactory httpClientFactory, IOptions<KubexApiSettings> kubexOptions)
    : IKubexHealthCheckService
{
    public async Task<HealthCheckResult> RunHealthCheckAsync(CancellationToken cancellationToken = default)
    {
        var settings = kubexOptions.Value;
        if (string.IsNullOrWhiteSpace(settings.BaseUrl) ||
            string.IsNullOrWhiteSpace(settings.Username) ||
            string.IsNullOrWhiteSpace(settings.Password))
        {
            throw new InvalidOperationException(
                "KubexApiSettings (BaseUrl, Username, Password) are not fully configured.");
        }

        var client = httpClientFactory.CreateClient("KubexApi");
        client.BaseAddress = new Uri(settings.BaseUrl.TrimEnd('/') + "/");

        var token = await AuthorizeAsync(client, settings, cancellationToken);
        var clusters = await GetClustersAsync(client, token, cancellationToken);

        return new HealthCheckResult(BuildSummary(clusters), clusters.ToJsonString());
    }

    private static async Task<string> AuthorizeAsync(HttpClient client, KubexApiSettings settings, CancellationToken cancellationToken)
    {
        var body = new JsonObject
        {
            ["userName"] = settings.Username,
            ["pwd"] = settings.Password
        }.ToJsonString();

        using var content = new StringContent(body, Encoding.UTF8, "application/json");
        using var response = await client.PostAsync("api/v2/authorize", content, cancellationToken);

        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException(
                $"Kubex authorization failed with status {(int)response.StatusCode} ({response.ReasonPhrase}).");
        }

        var json = await response.Content.ReadAsStringAsync(cancellationToken);
        var node = JsonNode.Parse(json);
        var token = node?["apiToken"]?.GetValue<string>();

        if (string.IsNullOrWhiteSpace(token))
        {
            throw new InvalidOperationException("Kubex authorization response did not contain an apiToken.");
        }

        return token;
    }

    private static async Task<JsonArray> GetClustersAsync(HttpClient client, string token, CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Get, "api/v2/kubernetes/clusters");
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
        request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));

        using var response = await client.SendAsync(request, cancellationToken);

        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException(
                $"Kubex cluster list request failed with status {(int)response.StatusCode} ({response.ReasonPhrase}).");
        }

        var json = await response.Content.ReadAsStringAsync(cancellationToken);
        var node = JsonNode.Parse(json);

        return node?["items"] as JsonArray ?? [];
    }

    private static string BuildSummary(JsonArray clusters)
    {
        if (clusters.Count == 0)
        {
            return "Kubex Health Check: no clusters found.";
        }

        var lines = new List<string> { $"Kubex Health Check — {clusters.Count} cluster(s):" };

        foreach (var item in clusters)
        {
            var name = item?["cluster"]?.GetValue<string>() ?? "(unknown)";
            var nodeCount = item?["nodeCount"]?.ToString() ?? "?";
            var containerCount = item?["containerCount"]?.ToString() ?? "?";
            var kubernetesVersion = item?["kubernetesVersion"]?.GetValue<string>() ?? "?";
            var lastCollectionRaw = item?["lastCollectionTime"]?.GetValue<string>();

            var freshness = "unknown";
            if (DateTime.TryParse(lastCollectionRaw, out var lastCollection))
            {
                var age = DateTime.UtcNow - lastCollection.ToUniversalTime();
                freshness = age <= TimeSpan.FromHours(24)
                    ? "fresh"
                    : $"STALE ({age.TotalHours:F0}h old)";
            }

            lines.Add($"- {name}: nodes={nodeCount}, containers={containerCount}, k8s={kubernetesVersion}, data={freshness}");
        }

        return string.Join("\n", lines);
    }
}

public record HealthCheckResult(string DeterministicSummary, string ClusterDataJson);
