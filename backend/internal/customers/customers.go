// Package customers loads the list of Kubex clients this service can run
// a fleet report across — each with its own MCP URL and its own
// authorization token (unlike the single shared KubexMcpSettings token
// used for a one-off "@KubexAI <url> <skill>" command).
package customers

import (
	"encoding/csv"
	"net/url"
	"os"
	"strings"
)

type Customer struct {
	// Derived from the URL's host (e.g. "sandbox-mcp.kubex.ai") — the CSV
	// only carries the URL and the token, not a separate display name.
	Name               string
	McpUrl             string
	AuthorizationToken string
}

// Load reads the customer list from a CSV file: MCP URL in column A,
// authorization token in column B. A header row is optional — if the
// first row's column A doesn't look like a URL (doesn't start with
// "http"), it's treated as a header and skipped; otherwise every row is
// read as data.
func Load(path string) ([]Customer, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1 // tolerate ragged rows instead of hard-erroring
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var list []Customer
	for i, row := range rows {
		if len(row) < 1 {
			continue
		}
		mcpUrl := strings.TrimSpace(row[0])
		if mcpUrl == "" {
			continue
		}
		if i == 0 && !strings.HasPrefix(strings.ToLower(mcpUrl), "http") {
			// Looks like a header row (e.g. "url,authorizationToken") —
			// skip it rather than treating it as a bogus customer.
			continue
		}

		token := ""
		if len(row) > 1 {
			token = strings.TrimSpace(row[1])
		}

		list = append(list, Customer{
			Name:               displayName(mcpUrl),
			McpUrl:             mcpUrl,
			AuthorizationToken: token,
		})
	}
	return list, nil
}

func displayName(mcpUrl string) string {
	if parsed, err := url.Parse(mcpUrl); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return mcpUrl
}
