// Package skills loads every skills/<name>/SKILL.md into memory once at
// startup. Adding a new skill is just adding a new folder — no code
// change, no config edit, no build step beyond the one you'd already do
// to ship any change.
//
// Dispatch word = the folder name, case- and punctuation-insensitive
// ("onthisday", "OnThisDay", "on-this-day" and "onthisday?" all match the
// same registered skill). Every skill is always loaded and always
// matchable by Find() — a SKILL.md carrying a leading
// "<!-- dispatch:false -->" HTML comment is still loaded and still
// matchable, but its GenericallyDispatchable comes back false. That flag
// only matters to the plain no-MCP word-dispatch path in the claude
// package — a command with an MCP URL attached can use a dispatch:false
// skill just fine, since attaching the MCP server is exactly the thing
// the marker says the generic (non-MCP) path can't do on its own.
package skills

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const dispatchDisabledMarker = "dispatch:false"

var (
	nonAlphaNumeric    = regexp.MustCompile(`[^a-z0-9]`)
	modelMarkerPattern = regexp.MustCompile(`model:\s*(\S+)`)
)

type Skill struct {
	Slug                    string
	Instructions            string
	GenericallyDispatchable bool

	// Set from a "<!-- model:claude-haiku-4-5-20251001 -->" marker line —
	// lets a narrow, cheap skill run on a cheaper model than
	// ClaudeApiSettings:Model, without that override affecting every
	// other command. Empty means "use whatever model the caller was
	// already going to use."
	Model string
}

type Registry struct {
	bySlug map[string]*Skill
	all    []*Skill
}

// Load reads every skills/<name>/SKILL.md under skillsDirectory. A
// missing directory is not an error — it just means no skills are
// dispatchable, matching the old behavior of logging a warning and
// carrying on.
func Load(skillsDirectory string, logger *log.Logger) (*Registry, error) {
	r := &Registry{bySlug: map[string]*Skill{}}

	entries, err := os.ReadDir(skillsDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Printf("Skills directory not found at %s — no skills will be dispatchable.", skillsDirectory)
			return r, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		folderName := entry.Name()
		skillFile := filepath.Join(skillsDirectory, folderName, "SKILL.md")

		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}
		content := string(data)

		// Markers are read from a leading block of HTML comment lines
		// only (scanning stops at the first non-comment line), so a
		// skill can carry more than one — e.g. both
		// "<!-- dispatch:false -->" and "<!-- model:... -->".
		genericallyDispatchable := true
		model := ""
		trimmed := strings.TrimLeftFunc(content, unicode.IsSpace)
		for _, rawLine := range strings.Split(trimmed, "\n") {
			line := strings.TrimSpace(rawLine)
			if !strings.HasPrefix(line, "<!--") {
				break
			}

			if strings.Contains(strings.ToLower(line), dispatchDisabledMarker) {
				genericallyDispatchable = false
			}

			if m := modelMarkerPattern.FindStringSubmatch(line); m != nil {
				model = strings.TrimSpace(m[1])
			}
		}

		slug := normalize(folderName)
		if slug == "" {
			continue
		}

		skill := &Skill{
			Slug:                    slug,
			Instructions:            strings.TrimSpace(content),
			GenericallyDispatchable: genericallyDispatchable,
			Model:                   model,
		}
		r.bySlug[slug] = skill
		r.all = append(r.all, skill)

		if !genericallyDispatchable {
			logger.Printf("Skill '%s' is marked dispatch:false — only usable from an MCP-attached request.", folderName)
		}
		if model != "" {
			logger.Printf("Skill '%s' overrides the model to '%s'.", folderName, model)
		}
	}

	names := make([]string, len(r.all))
	for i, s := range r.all {
		names[i] = s.Slug
	}
	logger.Printf("Loaded %d skill(s) from %s: %s", len(r.all), skillsDirectory, strings.Join(names, ", "))

	return r, nil
}

// Find looks up a skill by the word a caller typed (e.g. the token right
// after "@KubexAI"). Matching is case- and punctuation-insensitive.
func (r *Registry) Find(word string) *Skill {
	slug := normalize(word)
	if slug == "" {
		return nil
	}
	return r.bySlug[slug]
}

func (r *Registry) All() []*Skill {
	return r.all
}

func normalize(value string) string {
	return nonAlphaNumeric.ReplaceAllString(strings.ToLower(value), "")
}
