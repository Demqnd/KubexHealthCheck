using KubexHealthCheck.Config;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.Filters;
using Microsoft.Extensions.Options;

namespace KubexHealthCheck.Filters;

public class ApiKeyAuthFilter(IOptions<ApiKeySettings> apiKeyOptions) : IActionFilter
{
    private const string HeaderName = "X-Api-Key";

    public void OnActionExecuting(ActionExecutingContext context)
    {
        var configuredKey = apiKeyOptions.Value.Key;
        if (string.IsNullOrWhiteSpace(configuredKey))
        {
            context.Result = new ObjectResult(new { message = "Server is missing ApiKeySettings:Key configuration." })
            {
                StatusCode = StatusCodes.Status500InternalServerError
            };
            return;
        }

        if (!context.HttpContext.Request.Headers.TryGetValue(HeaderName, out var providedKey) ||
            providedKey != configuredKey)
        {
            context.Result = new UnauthorizedObjectResult(new { message = $"Missing or invalid {HeaderName} header." });
        }
    }

    public void OnActionExecuted(ActionExecutedContext context)
    {
    }
}
