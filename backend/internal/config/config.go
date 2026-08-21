// Package config loads settings the same way the old .NET backend did:
// appsettings.json, then appsettings.{ASPNETCORE_ENVIRONMENT}.json (both
// optional beyond the base file), then appsettings.{env}.local.json
// (gitignored, for real local secrets), then environment variables using
// the same "Section__Key" double-underscore names the GitHub Actions
// workflows already set. Each layer only overwrites the fields it
// actually mentions, so later layers can override individual values
// without needing to repeat the whole file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	ApiKeySettings struct {
		Key string `json:"Key"`
	} `json:"ApiKeySettings"`

	WebhookSettings struct {
		DefaultUrl string `json:"DefaultUrl"`
	} `json:"WebhookSettings"`

	ClaudeApiSettings struct {
		ApiKey string `json:"ApiKey"`
		Model  string `json:"Model"`
	} `json:"ClaudeApiSettings"`

	KubexMcpSettings struct {
		AuthorizationToken string `json:"AuthorizationToken"`
	} `json:"KubexMcpSettings"`

	DataDirectory   string `json:"DataDirectory"`
	SkillsDirectory string `json:"SkillsDirectory"`

	// Path to the JSON file listing fleet-report customers (name +
	// per-customer MCP URL + auth token). Defaults to "customers.json"
	// next to the running binary if unset. Real tokens live in this
	// file, not in appsettings*.json, so it's gitignored — only
	// customers.json.example is checked in.
	CustomersFile string `json:"CustomersFile"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	cfg.ClaudeApiSettings.Model = "claude-opus-5"

	env := os.Getenv("ASPNETCORE_ENVIRONMENT")
	if env == "" {
		env = "Production"
	}

	files := []string{
		"appsettings.json",
		fmt.Sprintf("appsettings.%s.json", env),
		fmt.Sprintf("appsettings.%s.local.json", env),
	}

	for _, file := range files {
		if err := mergeJSONFile(cfg, file); err != nil {
			return nil, fmt.Errorf("loading %s: %w", file, err)
		}
	}

	applyEnvOverrides(cfg)
	return cfg, nil
}

func mergeJSONFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	// Unmarshalling into the already-populated cfg only overwrites fields
	// present in this file's JSON, leaving everything else as the previous
	// layer left it — that's what gives layered config files their
	// "override just what you mention" behavior.
	return json.Unmarshal(data, cfg)
}

func applyEnvOverrides(cfg *Config) {
	if v, ok := os.LookupEnv("ApiKeySettings__Key"); ok {
		cfg.ApiKeySettings.Key = v
	}
	if v, ok := os.LookupEnv("WebhookSettings__DefaultUrl"); ok {
		cfg.WebhookSettings.DefaultUrl = v
	}
	if v, ok := os.LookupEnv("ClaudeApiSettings__ApiKey"); ok {
		cfg.ClaudeApiSettings.ApiKey = v
	}
	if v, ok := os.LookupEnv("ClaudeApiSettings__Model"); ok {
		cfg.ClaudeApiSettings.Model = v
	}
	if v, ok := os.LookupEnv("KubexMcpSettings__AuthorizationToken"); ok {
		cfg.KubexMcpSettings.AuthorizationToken = v
	}
	if v, ok := os.LookupEnv("DataDirectory"); ok {
		cfg.DataDirectory = v
	}
	if v, ok := os.LookupEnv("SkillsDirectory"); ok {
		cfg.SkillsDirectory = v
	}
	if v, ok := os.LookupEnv("CustomersFile"); ok {
		cfg.CustomersFile = v
	}
}
