using System.Text.Json;

namespace KubexHealthCheck.Services;

public class JsonFileWebhookRoutineStore : IWebhookRoutineStore
{
    private readonly string _filePath;
    private readonly SemaphoreSlim _lock = new(1, 1);

    public JsonFileWebhookRoutineStore(IConfiguration configuration, IHostEnvironment environment)
    {
        var dataDirectory = configuration["DataDirectory"];
        if (string.IsNullOrWhiteSpace(dataDirectory))
        {
            dataDirectory = Path.Combine(environment.ContentRootPath, "data");
        }

        Directory.CreateDirectory(dataDirectory);
        _filePath = Path.Combine(dataDirectory, "webhook-routine.json");
    }

    public async Task<WebhookRoutine> GetAsync(CancellationToken cancellationToken = default)
    {
        await _lock.WaitAsync(cancellationToken);
        try
        {
            return await ReadAsync(cancellationToken);
        }
        finally
        {
            _lock.Release();
        }
    }

    public async Task<WebhookRoutine> SaveAsync(string url, CancellationToken cancellationToken = default)
    {
        await _lock.WaitAsync(cancellationToken);
        try
        {
            var routine = new WebhookRoutine { Url = url, UpdatedAtUtc = DateTime.UtcNow };
            var json = JsonSerializer.Serialize(routine, new JsonSerializerOptions { WriteIndented = true });
            await File.WriteAllTextAsync(_filePath, json, cancellationToken);
            return routine;
        }
        finally
        {
            _lock.Release();
        }
    }

    private async Task<WebhookRoutine> ReadAsync(CancellationToken cancellationToken)
    {
        if (!File.Exists(_filePath))
        {
            return new WebhookRoutine();
        }

        var json = await File.ReadAllTextAsync(_filePath, cancellationToken);
        if (string.IsNullOrWhiteSpace(json))
        {
            return new WebhookRoutine();
        }

        return JsonSerializer.Deserialize<WebhookRoutine>(json) ?? new WebhookRoutine();
    }
}
