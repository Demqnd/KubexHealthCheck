// Package skills loads every skills/<name>/SKILL.md into memory once at
// startup. Adding a new skill is just adding a new folder — no code
// change, no config edit, no build step beyond the one you'd already do
// to ship any change.
//
// Dispatch word = the folder name, case- and punctuation-insensitive
// ("onthisday", "OnThisDay", "on-this-day" and "onthisday?" all match the
// same registered skill).
package skills

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nonAlphaNumeric = regexp.MustCompile(`[^a-z0-9]`)

type Skill struct {
	Slug         string
	Instructions string
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

		slug := normalize(folderName)
		if slug == "" {
			continue
		}

		skill := &Skill{
			Slug:         slug,
			Instructions: strings.TrimSpace(string(data)),
		}
		r.bySlug[slug] = skill
		r.all = append(r.all, skill)
	}

	names := make([]string, len(r.all))
	for i, s := range r.all {
		names[i] = s.Slug
	}
	logger.Printf("Loaded %d skill(s) from %s: %s", len(r.all), skillsDirectory, strings.Join(names, ", "))

	return r, nil
}

// Find looks up a skill by the word a caller typed. Matching is case-
// and punctuation-insensitive.
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
