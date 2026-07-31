using System.Text.Json;
using KubexHealthCheck.Config;
using Microsoft.Extensions.Options;

namespace KubexHealthCheck.Services;

public class JsonFileWebhookRoutineStore : IWebhookRoutineStore
{
    private readonly string _filePath;
    private readonly IOptions<WebhookSettings> _webhookOptions;
    private readonly SemaphoreSlim _lock = new(1, 1);

    public JsonFileWebhookRoutineStore(
        IConfiguration configuration,
        IHostEnvironment environment,
        IOptions<WebhookSettings> webhookOptions)
    {
        var dataDirectory = configuration["DataDirectory"];
        if (string.IsNullOrWhiteSpace(dataDirectory))
        {
            dataDirectory = Path.Combine(environment.ContentRootPath, "data");
        }

        Directory.CreateDirectory(dataDirectory);
        _filePath = Path.Combine(dataDirectory, "webhook-routine.json");
        _webhookOptions = webhookOptions;
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
            return DefaultRoutine();
        }

        var json = await File.ReadAllTextAsync(_filePath, cancellationToken);
        if (string.IsNullOrWhiteSpace(json))
        {
            return DefaultRoutine();
        }

        var routine = JsonSerializer.Deserialize<WebhookRoutine>(json) ?? new WebhookRoutine();
        return string.IsNullOrWhiteSpace(routine.Url) ? DefaultRoutine() : routine;
    }

    private WebhookRoutine DefaultRoutine() => new() { Url = _webhookOptions.Value.DefaultUrl };
}
