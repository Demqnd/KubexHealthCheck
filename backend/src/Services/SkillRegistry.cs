using System.Text.RegularExpressions;

namespace KubexHealthCheck.Services;

// Loads every skills/<name>/SKILL.md into memory once at startup. Adding a new
// skill is just adding a new folder — no code change, no restart-triggering
// config edit, no build step beyond the one you'd already do to ship any change.
//
// Dispatch word = the folder name, case- and punctuation-insensitive
// ("onthisday", "OnThisDay", "on-this-day" and "onthisday?" all match the same
// registered skill). A SKILL.md whose first line is the HTML comment
// "<!-- dispatch:false -->" is loaded for documentation but never dispatched —
// use that for a skill (like kubex-health-check) whose real logic needs code
// this generic path can't provide (e.g. MCP tool wiring), so a bare skill-word
// match can't silently produce a half-working answer.
public class SkillRegistry : ISkillRegistry
{
    private const string DispatchDisabledMarker = "dispatch:false";
    private static readonly Regex NonAlphaNumeric = new("[^a-z0-9]", RegexOptions.Compiled);

    private readonly Dictionary<string, Skill> _bySlug = new(StringComparer.Ordinal);
    private readonly List<Skill> _all = [];

    public SkillRegistry(IConfiguration configuration, IHostEnvironment environment, ILogger<SkillRegistry> logger)
    {
        var skillsDirectory = configuration["SkillsDirectory"];
        if (string.IsNullOrWhiteSpace(skillsDirectory))
        {
            // ContentRootPath is backend/ when run the normal way (dotnet run
            // from that folder, same as every workflow in .github/workflows/
            // does) — skills/ is its sibling at the repo root.
            skillsDirectory = Path.GetFullPath(Path.Combine(environment.ContentRootPath, "..", "skills"));
        }

        if (!Directory.Exists(skillsDirectory))
        {
            logger.LogWarning("Skills directory not found at {Path} — no skills will be dispatchable.", skillsDirectory);
            return;
        }

        foreach (var skillDir in Directory.GetDirectories(skillsDirectory))
        {
            var skillFile = Path.Combine(skillDir, "SKILL.md");
            if (!File.Exists(skillFile))
            {
                continue;
            }

            var folderName = Path.GetFileName(skillDir);

            try
            {
                var content = File.ReadAllText(skillFile);
                var firstLine = content.TrimStart().Split('\n', 2)[0];
                if (firstLine.Contains(DispatchDisabledMarker, StringComparison.OrdinalIgnoreCase))
                {
                    logger.LogInformation("Skill '{Folder}' is marked dispatch:false — loaded for docs only.", folderName);
                    continue;
                }

                var slug = Normalize(folderName);
                if (string.IsNullOrEmpty(slug))
                {
                    continue;
                }

                var skill = new Skill { Slug = slug, Instructions = content.Trim() };
                _bySlug[slug] = skill;
                _all.Add(skill);
            }
            catch (IOException ex)
            {
                logger.LogError(ex, "Failed to read skill file {File}", skillFile);
            }
        }

        logger.LogInformation(
            "Loaded {Count} dispatchable skill(s) from {Path}: {Slugs}",
            _all.Count, skillsDirectory, string.Join(", ", _all.Select(s => s.Slug)));
    }

    public Skill? Find(string word)
    {
        var slug = Normalize(word);
        return string.IsNullOrEmpty(slug) ? null : _bySlug.GetValueOrDefault(slug);
    }

    public IReadOnlyCollection<Skill> All => _all;

    private static string Normalize(string value) => NonAlphaNumeric.Replace(value.ToLowerInvariant(), "");
}
