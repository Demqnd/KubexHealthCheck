namespace KubexHealthCheck.Services;

public interface IClaudeSummaryService
{
    Task<string> SummarizeHealthCheckAsync(string clusterDataJson, CancellationToken cancellationToken = default);

    Task<string> AskAsync(string apiKey, string question, CancellationToken cancellationToken = default);
}
