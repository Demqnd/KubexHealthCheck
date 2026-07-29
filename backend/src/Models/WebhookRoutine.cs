namespace KubexHealthCheck.Models;

public class WebhookRoutine
{
    public int Id { get; set; }
    public string Url { get; set; } = string.Empty;
    public DateTime UpdatedAtUtc { get; set; } = DateTime.UtcNow;
}
