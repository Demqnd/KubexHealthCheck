namespace KubexHealthCheck.Services;

public class WebhookRoutine
{
    public string Url { get; set; } = string.Empty;
    public DateTime? UpdatedAtUtc { get; set; }
}
