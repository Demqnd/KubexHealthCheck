namespace KubexHealthCheck.Services;

public interface IClaudeSummaryService
{
    Task<string> SummarizeHealthCheckAsync(string clusterDataJson, CancellationToken cancellationToken = default);

    Task<string> AskAsync(string apiKey, string question, CancellationToken cancellationToken = default);

    Task<string> RunCommandAsync(string command, CancellationToken cancellationToken = default);
}
