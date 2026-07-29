using KubexHealthCheck.Contracts;
using KubexHealthCheck.Services;
using Microsoft.AspNetCore.Mvc;

namespace KubexHealthCheck.Controllers;

[ApiController]
[Route("api/[controller]")]
public class WebhookController(IWebhookRoutineStore webhookRoutineStore, IWebhookMessageSender webhookMessageSender) : ControllerBase
{
    [HttpGet]
    public async Task<IActionResult> GetRoutine()
    {
        var routine = await webhookRoutineStore.GetAsync();
        return Ok(new WebhookRoutineDto(routine.Url, routine.UpdatedAtUtc));
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

        var routine = await webhookRoutineStore.SaveAsync(url);
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

        var routine = await webhookRoutineStore.GetAsync();
        if (string.IsNullOrWhiteSpace(routine.Url))
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
