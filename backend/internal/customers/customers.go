// Package customers loads the list of Kubex clients this service can run
// a fleet report across — each with its own MCP URL and its own login
// credentials (unlike the single shared KubexMcpSettings token used for
// a one-off "@KubexAI <url> <skill>" command).
//
// Username/password sign-in (internal/kubexauth) replaces a manually
// obtained, quickly-expiring MCP token: instead of pasting a token in
// here per customer, each row carries a username/password that gets
// signed in at call time.
package customers

import (
	"encoding/csv"
	"os"
	"strings"
)

type Customer struct {
	Name     string
	McpUrl   string
	Username string
	Password string
}

// Load reads the customer list from a CSV file: name in column A, MCP
// URL in column B, username in column C, password in column D. A header
// row is optional — if the first row's column B doesn't look like a URL
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
			// Looks like a header row (e.g. "name,mcpUrl,username,password")
			// — skip it rather than treating it as a bogus customer.
			continue
		}

		username := ""
		if len(row) > 2 {
			username = strings.TrimSpace(row[2])
		}
		password := ""
		if len(row) > 3 {
			password = strings.TrimSpace(row[3])
		}

		if name == "" {
			name = mcpUrl
		}

		list = append(list, Customer{
			Name:     name,
			McpUrl:   mcpUrl,
			Username: username,
			Password: password,
		})
	}
	return list, nil
}
