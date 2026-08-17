namespace KubexHealthCheck.Services;

// One loaded skill: the word that dispatches it, and the full contents of its
// SKILL.md, used verbatim as the system prompt when that word is matched.
public class Skill
{
    public required string Slug { get; init; }

    public required string Instructions { get; init; }

    // False for a skill marked "<!-- dispatch:false -->" — it's still loaded
    // and still matchable by word, but only from a context that can supply
    // whatever the marker says the generic dispatcher can't (e.g. an MCP
    // server already attached to the request). The plain no-MCP dispatch
    // path in ClaudeSummaryService checks this before using a matched skill;
    // the MCP-URL path does not, since it's exactly the context the marker
    // is about.
    public required bool GenericallyDispatchable { get; init; }

    // Set from a "<!-- model:claude-haiku-4-5-20251001 -->" marker line —
    // lets a narrow, cheap skill (e.g. one that only needs to count entries
    // in a tool result) run on a cheaper model than ClaudeApiSettings:Model,
    // without that override affecting every other command. Null means "use
    // whatever model the caller was already going to use."
    public string? Model { get; init; }
}
