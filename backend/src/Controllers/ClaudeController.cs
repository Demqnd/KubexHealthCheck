using KubexHealthCheck.Contracts;
using KubexHealthCheck.Services;
using Microsoft.AspNetCore.Mvc;

namespace KubexHealthCheck.Controllers;

[ApiController]
[Route("api/[controller]")]
public class ClaudeController(
    IClaudeSummaryService claudeSummaryService,
    IWebhookRoutineStore webhookRoutineStore,
    IWebhookMessageSender webhookMessageSender) : ControllerBase
{
    [HttpPost("ask")]
    public async Task<IActionResult> Ask(AskClaudeRequest request)
    {
        var apiKey = request.ApiKey?.Trim();
        var question = request.Question?.Trim();

        if (string.IsNullOrWhiteSpace(apiKey))
        {
            return BadRequest(new { message = "An Anthropic API key is required." });
        }

        if (string.IsNullOrWhiteSpace(question))
        {
            return BadRequest(new { message = "A question is required." });
        }

        string answer;
        try
        {
            answer = await claudeSummaryService.AskAsync(apiKey, question);
        }
        catch (Exception ex)
        {
            return StatusCode(502, new { message = $"Claude request failed: {ex.Message}" });
        }

        var postedToTeams = false;
        string? postError = null;

        if (request.PostToTeams)
        {
            var routine = await webhookRoutineStore.GetAsync();
            if (string.IsNullOrWhiteSpace(routine.Url))
            {
                postError = "No webhook URL has been configured yet.";
            }
            else
            {
                try
                {
                    await webhookMessageSender.SendAsync(routine.Url, answer);
                    postedToTeams = true;
                }
                catch (Exception ex)
                {
                    postError = $"Failed to post to webhook: {ex.Message}";
                }
            }
        }

        return Ok(new { answer, postedToTeams, postError });
    }
}
