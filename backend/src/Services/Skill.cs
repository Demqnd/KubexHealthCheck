namespace KubexHealthCheck.Services;

// One loaded skill: the word that dispatches it, and the full contents of its
// SKILL.md, used verbatim as the system prompt when that word is matched.
public class Skill
{
    public required string Slug { get; init; }

    public required string Instructions { get; init; }
}
