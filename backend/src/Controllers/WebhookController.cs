using KubexHealthCheck.Contracts;
using KubexHealthCheck.Data;
using KubexHealthCheck.Filters;
using KubexHealthCheck.Models;
using KubexHealthCheck.Services;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

namespace KubexHealthCheck.Controllers;

[ApiController]
[Route("api/[controller]")]
[ServiceFilter(typeof(ApiKeyAuthFilter))]
public class WebhookController(AppDbContext dbContext, IWebhookMessageSender webhookMessageSender) : ControllerBase
{
    [HttpGet]
    public async Task<IActionResult> GetRoutine()
    {
        var routine = await dbContext.WebhookRoutines.FirstOrDefaultAsync();
        return Ok(new WebhookRoutineDto(routine?.Url ?? string.Empty, routine?.UpdatedAtUtc));
    }

    [HttpPut]
    public async Task<IActionResult> UpdateRoutine(UpdateWebhookRoutineRequest request)
    {
        var url = request.Url?.Trim() ?? string.Empty;
        if (string.IsNullOrWhiteSpace(url) ||
            !Uri.TryCreate(url, UriKind.Absolute, out var uri) ||
            (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps))
        {
            return BadRequest(new { message = "A valid http or https webhook URL is required." });
        }

        var routine = await dbContext.WebhookRoutines.FirstOrDefaultAsync();
        if (routine is null)
        {
            routine = new WebhookRoutine();
            dbContext.WebhookRoutines.Add(routine);
        }

        routine.Url = url;
        routine.UpdatedAtUtc = DateTime.UtcNow;
        await dbContext.SaveChangesAsync();

        return Ok(new WebhookRoutineDto(routine.Url, routine.UpdatedAtUtc));
    }

    [HttpPost("send")]
    public async Task<IActionResult> SendMessage(SendWebhookMessageRequest request)
    {
        var message = request.Message?.Trim();
        if (string.IsNullOrWhiteSpace(message))
        {
            return BadRequest(new { message = "A message is required." });
        }

        var routine = await dbContext.WebhookRoutines.FirstOrDefaultAsync();
        if (routine is null || string.IsNullOrWhiteSpace(routine.Url))
        {
            return BadRequest(new { message = "No webhook URL has been configured yet." });
        }

        try
        {
            await webhookMessageSender.SendAsync(routine.Url, message);
        }
        catch (Exception ex)
        {
            return StatusCode(502, new { message = $"Failed to deliver webhook message: {ex.Message}" });
        }

        return Ok(new { message = "Message sent." });
    }
}
