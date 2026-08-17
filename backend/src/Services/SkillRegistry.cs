using System.Text.RegularExpressions;

namespace KubexHealthCheck.Services;

// Loads every skills/<name>/SKILL.md into memory once at startup. Adding a new
// skill is just adding a new folder — no code change, no restart-triggering
// config edit, no build step beyond the one you'd already do to ship any change.
//
// Dispatch word = the folder name, case- and punctuation-insensitive
// ("onthisday", "OnThisDay", "on-this-day" and "onthisday?" all match the same
// registered skill). Every skill is always loaded and always matchable by
// Find() — a SKILL.md whose first line is the HTML comment
// "<!-- dispatch:false -->" is still loaded and still matchable, but its
// Skill.GenericallyDispatchable comes back false. That flag only matters to
// the plain no-MCP word-dispatch path in ClaudeSummaryService — a command
// with an MCP URL attached can use a dispatch:false skill just fine, since
// attaching the MCP server is exactly the thing the marker says the generic
// (non-MCP) path can't do on its own.
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
                var genericallyDispatchable = !firstLine.Contains(DispatchDisabledMarker, StringComparison.OrdinalIgnoreCase);

                var slug = Normalize(folderName);
                if (string.IsNullOrEmpty(slug))
                {
                    continue;
                }

                var skill = new Skill
                {
                    Slug = slug,
                    Instructions = content.Trim(),
                    GenericallyDispatchable = genericallyDispatchable,
                };
                _bySlug[slug] = skill;
                _all.Add(skill);

                if (!genericallyDispatchable)
                {
                    logger.LogInformation("Skill '{Folder}' is marked dispatch:false — only usable from an MCP-attached request.", folderName);
                }
            }
            catch (IOException ex)
            {
                logger.LogError(ex, "Failed to read skill file {File}", skillFile);
            }
        }

        logger.LogInformation(
            "Loaded {Count} skill(s) from {Path}: {Slugs}",
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
