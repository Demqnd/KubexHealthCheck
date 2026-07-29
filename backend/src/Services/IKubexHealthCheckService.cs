namespace KubexHealthCheck.Services;

public interface IKubexHealthCheckService
{
    Task<HealthCheckResult> RunHealthCheckAsync(CancellationToken cancellationToken = default);
}
