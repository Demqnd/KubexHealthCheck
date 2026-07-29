using KubexHealthCheck.Services;
using Microsoft.AspNetCore.Mvc;

namespace KubexHealthCheck.Controllers;

[ApiController]
[Route("api/[controller]")]
public class KubexHealthCheckController(
    IWebhookRoutineStore webhookRoutineStore,
    IKubexHealthCheckService kubexHealthCheckService,
    IClaudeSummaryService claudeSummaryService,
    IWebhookMessageSender webhookMessageSender) : ControllerBase
{
    [HttpPost("run")]
    public async Task<IActionResult> Run()
    {
        HealthCheckResult result;
        try
        {
            result = await kubexHealthCheckService.RunHealthCheckAsync();
        }
        catch (Exception ex)
        {
            return StatusCode(502, new { message = $"Kubex health check failed: {ex.Message}" });
        }

        var summary = result.DeterministicSummary;
        var usedAi = false;
        try
        {
            summary = await claudeSummaryService.SummarizeHealthCheckAsync(result.ClusterDataJson);
            usedAi = true;
        }
        catch
        {
            // Fall back to the deterministic summary if Claude isn't configured or the call fails.
        }

        var routine = await webhookRoutineStore.GetAsync();
        if (string.IsNullOrWhiteSpace(routine.Url))
        {
            return BadRequest(new { message = "No webhook URL has been configured yet.", summary, usedAi });
        }

        try
        {
            await webhookMessageSender.SendAsync(routine.Url, summary);
        }
        catch (Exception ex)
        {
            return StatusCode(502, new { message = $"Failed to post health check to webhook: {ex.Message}", summary, usedAi });
        }

        return Ok(new { message = "Health check posted to webhook.", summary, usedAi });
    }
}
