using KubexHealthCheck.Contracts;
using KubexHealthCheck.Filters;
using KubexHealthCheck.Services;
using Microsoft.AspNetCore.Mvc;

namespace KubexHealthCheck.Controllers;

[ApiController]
[Route("api/[controller]")]
[ServiceFilter(typeof(ApiKeyAuthFilter))]
public class ClaudeController(
    IClaudeSummaryService claudeSummaryService,
    IWebhookRoutineStore webhookRoutineStore,
    IWebhookMessageSender webhookMessageSender) : ControllerBase
{
    [HttpPost("command")]
    public async Task<IActionResult> Command(ClaudeCommandRequest request)
    {
        var command = request.Command?.Trim();
        if (string.IsNullOrWhiteSpace(command))
        {
            return BadRequest(new { message = "A command is required." });
        }

        string response;
        try
        {
            response = await claudeSummaryService.RunCommandAsync(command);
        }
        catch (Exception ex)
        {
            return StatusCode(502, new { message = $"Claude request failed: {ex.Message}" });
        }

        var postedToTeams = false;
        string? postError = null;

        var routine = await webhookRoutineStore.GetAsync();
        if (string.IsNullOrWhiteSpace(routine.Url))
        {
            postError = "No webhook URL has been configured yet.";
        }
        else
        {
            try
            {
                await webhookMessageSender.SendAsync(routine.Url, response);
                postedToTeams = true;
            }
            catch (Exception ex)
            {
                postError = $"Failed to post to webhook: {ex.Message}";
            }
        }

        return Ok(new { response, postedToTeams, postError });
    }

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
