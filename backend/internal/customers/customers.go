// Package customers loads the list of Kubex clients this service can run
// a fleet report across — each with its own MCP URL and its own
// authorization token (unlike the single shared KubexMcpSettings token
// used for a one-off "@KubexAI <url> <skill>" command).
//
// This is the token-based version: each customer's token has to be
// obtained manually (e.g. via the MCP Inspector) and pasted in here.
// internal/kubexauth has an alternate, username/password-based sign-in
// path for automating that — not wired in here yet, pending Kubex
// account setup (the "API-enabled" flag) needed to test it end-to-end.
package customers

import (
	"encoding/csv"
	"os"
	"strings"
)

type Customer struct {
	Name               string
	McpUrl             string
	AuthorizationToken string
}

// Load reads the customer list from a CSV file: name in column A, MCP
// URL in column B, authorization token in column C. A header row is
// optional — if the first row's column B doesn't look like a URL
// (doesn't start with "http"), it's treated as a header and skipped;
// otherwise every row is read as data.
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
		if len(row) < 2 {
			continue
		}
		name := strings.TrimSpace(row[0])
		mcpUrl := strings.TrimSpace(row[1])
		if mcpUrl == "" {
			continue
		}
		if i == 0 && !strings.HasPrefix(strings.ToLower(mcpUrl), "http") {
			// Looks like a header row (e.g. "name,mcpUrl,authorizationToken")
			// — skip it rather than treating it as a bogus customer.
			continue
		}

		token := ""
		if len(row) > 2 {
			token = strings.TrimSpace(row[2])
		}

		if name == "" {
			name = mcpUrl
		}

		list = append(list, Customer{
			Name:               name,
			McpUrl:             mcpUrl,
			AuthorizationToken: token,
		})
	}
	return list, nil
}
