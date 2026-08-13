namespace KubexHealthCheck.Services;

public interface ISkillRegistry
{
    // Looks up a skill by the word a caller typed (e.g. the token right after
    // "@KubexAI"). Matching is case- and punctuation-insensitive.
    Skill? Find(string word);

    IReadOnlyCollection<Skill> All { get; }
}
