namespace KubexHealthCheck.Contracts;

public record AskClaudeRequest(string ApiKey, string Question, bool PostToTeams);
