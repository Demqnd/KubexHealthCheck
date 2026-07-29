using KubexHealthCheck.Config;
using KubexHealthCheck.Services;

var builder = WebApplication.CreateBuilder(args);

builder.Services.AddControllers();
builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen();

builder.Services.AddCors(options =>
{
    options.AddPolicy("AllowFrontend", policy =>
    {
        policy.WithOrigins("http://localhost:3000", "http://localhost:8080")
            .AllowAnyMethod()
            .AllowAnyHeader();
    });
});

builder.Services.Configure<KubexApiSettings>(builder.Configuration.GetSection("KubexApiSettings"));
builder.Services.Configure<ClaudeApiSettings>(builder.Configuration.GetSection("ClaudeApiSettings"));

builder.Services.AddHttpClient("Webhook", client =>
{
    client.Timeout = TimeSpan.FromSeconds(15);
});
builder.Services.AddHttpClient("KubexApi", client =>
{
    client.Timeout = TimeSpan.FromSeconds(30);
});
builder.Services.AddHttpClient("ClaudeApi", client =>
{
    client.Timeout = TimeSpan.FromSeconds(30);
});

builder.Services.AddSingleton<IWebhookRoutineStore, JsonFileWebhookRoutineStore>();
builder.Services.AddScoped<IWebhookMessageSender, WebhookMessageSender>();
builder.Services.AddScoped<IKubexHealthCheckService, KubexHealthCheckService>();
builder.Services.AddScoped<IClaudeSummaryService, ClaudeSummaryService>();

var app = builder.Build();

app.UseSwagger();
app.UseSwaggerUI();

app.UseCors("AllowFrontend");
app.MapControllers();

app.Run();
