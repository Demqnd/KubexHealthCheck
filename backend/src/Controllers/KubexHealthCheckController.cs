using KubexHealthCheck.Data;
using KubexHealthCheck.Filters;
using KubexHealthCheck.Services;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

namespace KubexHealthCheck.Controllers;

[ApiController]
[Route("api/[controller]")]
[ServiceFilter(typeof(ApiKeyAuthFilter))]
public class KubexHealthCheckController(
    AppDbContext dbContext,
    IKubexHealthCheckService kubexHealthCheckService,
    IWebhookMessageSender webhookMessageSender) : ControllerBase
{
    [HttpPost("run")]
    public async Task<IActionResult> Run()
    {
        string summary;
        try
        {
            summary = await kubexHealthCheckService.RunHealthCheckAsync();
        }
        catch (Exception ex)
        {
            return StatusCode(502, new { message = $"Kubex health check failed: {ex.Message}" });
        }

        var routine = await dbContext.WebhookRoutines.FirstOrDefaultAsync();
        if (routine is null || string.IsNullOrWhiteSpace(routine.Url))
        {
            return BadRequest(new { message = "No webhook URL has been configured yet.", summary });
        }

        try
        {
            await webhookMessageSender.SendAsync(routine.Url, summary);
        }
        catch (Exception ex)
        {
            return StatusCode(502, new { message = $"Failed to post health check to webhook: {ex.Message}", summary });
        }

        return Ok(new { message = "Health check posted to webhook.", summary });
    }
}
