namespace KubexHealthCheck.Services;

public interface IKubexHealthCheckService
{
    Task<string> RunHealthCheckAsync(CancellationToken cancellationToken = default);
}
