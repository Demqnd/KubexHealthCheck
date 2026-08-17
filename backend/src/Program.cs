using KubexHealthCheck.Config;
using KubexHealthCheck.Filters;
using KubexHealthCheck.Services;
using Microsoft.OpenApi;

var builder = WebApplication.CreateBuilder(args);

// Gitignored (see .gitignore's "appsettings.*.local.json" pattern) — put real local secrets
// here instead of in the tracked appsettings.json / appsettings.{Environment}.json files.
builder.Configuration.AddJsonFile(
    $"appsettings.{builder.Environment.EnvironmentName}.local.json", optional: true, reloadOnChange: true);

builder.Services.AddControllers();
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen(options =>
{
    options.AddSecurityDefinition("ApiKey", new OpenApiSecurityScheme
    {
        Name = "X-Api-Key",
        Type = SecuritySchemeType.ApiKey,
        In = ParameterLocation.Header,
        Description = "API key configured in ApiKeySettings:Key"
    });

    options.AddSecurityRequirement(document => new OpenApiSecurityRequirement
    {
        [new OpenApiSecuritySchemeReference("ApiKey", document, null)] = new List<string>()
    });
});

builder.Services.AddCors(options =>
{
    options.AddPolicy("AllowFrontend", policy =>
    {
        // The frontend is a static file opened directly (file://), which sends
        // Origin: null — so any-origin is required rather than an allowlist.
        policy.AllowAnyOrigin()
            .AllowAnyMethod()
            .AllowAnyHeader();
    });
});

builder.Services.Configure<ApiKeySettings>(builder.Configuration.GetSection("ApiKeySettings"));
builder.Services.Configure<ClaudeApiSettings>(builder.Configuration.GetSection("ClaudeApiSettings"));
builder.Services.Configure<WebhookSettings>(builder.Configuration.GetSection("WebhookSettings"));
builder.Services.Configure<KubexMcpSettings>(builder.Configuration.GetSection("KubexMcpSettings"));

builder.Services.AddHttpClient("Webhook", client =>
{
    client.Timeout = TimeSpan.FromSeconds(15);
});
builder.Services.AddHttpClient("ClaudeApi", client =>
{
    client.Timeout = TimeSpan.FromSeconds(30);
});

builder.Services.AddSingleton<IWebhookRoutineStore, JsonFileWebhookRoutineStore>();
builder.Services.AddSingleton<ISkillRegistry, SkillRegistry>();
builder.Services.AddScoped<IWebhookMessageSender, WebhookMessageSender>();
builder.Services.AddScoped<IClaudeSummaryService, ClaudeSummaryService>();
builder.Services.AddScoped<ApiKeyAuthFilter>();

var app = builder.Build();

app.UseSwagger();
app.UseSwaggerUI();

app.UseCors("AllowFrontend");
app.MapControllers();

app.Run();
