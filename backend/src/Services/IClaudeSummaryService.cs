namespace KubexHealthCheck.Services;

public interface IClaudeSummaryService
{
    Task<string> SummarizeHealthCheckAsync(string clusterDataJson, CancellationToken cancellationToken = default);
}
