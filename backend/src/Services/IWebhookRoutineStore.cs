namespace KubexHealthCheck.Services;

public interface IWebhookRoutineStore
{
    Task<WebhookRoutine> GetAsync(CancellationToken cancellationToken = default);
    Task<WebhookRoutine> SaveAsync(string url, CancellationToken cancellationToken = default);
}
