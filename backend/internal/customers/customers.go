// Package customers loads the list of Kubex clients this service can run
// a fleet report across — each with its own MCP URL and its own
// authorization token (unlike the single shared KubexMcpSettings token
// used for a one-off "@KubexAI <url> <skill>" command).
package customers

import (
	"encoding/json"
	"os"
)

type Customer struct {
	Name               string `json:"name"`
	McpUrl             string `json:"mcpUrl"`
	AuthorizationToken string `json:"authorizationToken"`
}

// Load reads the customer list from path. A missing file is not an
// error — it just means no customers are configured yet, matching how
// SkillsDirectory behaves when missing.
func Load(path string) ([]Customer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	var list []Customer
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}
