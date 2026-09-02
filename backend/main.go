package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"kubexhealthcheck/internal/api"
	"kubexhealthcheck/internal/claude"
	"kubexhealthcheck/internal/config"
	"kubexhealthcheck/internal/skills"
	"kubexhealthcheck/internal/webhook"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Diagnostic only — never logs the token value itself, just whether
	// config.Load() actually found one. appsettings*.json is read
	// relative to the process's current working directory, so a wrong
	// cwd (or wrong ASPNETCORE_ENVIRONMENT) silently loads no file at
	// all rather than erroring, which looks identical to "empty token"
	// from the outside.
	if wd, err := os.Getwd(); err == nil {
		log.Printf(
			"Config loaded from cwd=%s env=%s: KubexMcpSettings.AuthorizationToken set=%v (len=%d)",
			wd, envOrDefault("ASPNETCORE_ENVIRONMENT", "Production"),
			cfg.KubexMcpSettings.AuthorizationToken != "", len(cfg.KubexMcpSettings.AuthorizationToken))
	}

	skillsDirectory := cfg.SkillsDirectory
	if skillsDirectory == "" {
		// The process runs from backend/ (same as every workflow in
		// .github/workflows/ does) — skills/ is its sibling at the repo
		// root.
		wd, err := os.Getwd()
		if err != nil {
			log.Fatalf("failed to resolve working directory: %v", err)
		}
		skillsDirectory, err = filepath.Abs(filepath.Join(wd, "..", "skills"))
		if err != nil {
			log.Fatalf("failed to resolve skills directory: %v", err)
		}
	}

	skillRegistry, err := skills.Load(skillsDirectory, log.Default())
	if err != nil {
		log.Fatalf("failed to load skills: %v", err)
	}

	webhookStore, err := webhook.NewStore(cfg.DataDirectory, cfg.WebhookSettings.DefaultUrl)
	if err != nil {
		log.Fatalf("failed to open webhook store: %v", err)
	}
	webhookSender := webhook.NewSender()

	customersFile := cfg.CustomersFile
	if customersFile == "" {
		customersFile = "customers.csv"
	}

	claudeService := claude.NewService(cfg, skillRegistry, customersFile)

	server := api.NewServer(cfg, claudeService, webhookStore, webhookSender)

	addr := os.Getenv("ASPNETCORE_URLS")
	if addr == "" {
		addr = "http://localhost:5000"
	}
	addr = stripScheme(addr)

	log.Printf("Listening on %s", addr)
	if err := http.ListenAndServe(addr, server); err != nil {
		log.Fatal(err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ASPNETCORE_URLS (kept as the env var name for parity with the old
// backend, since the GitHub Actions workflows already set it) carries a
// scheme like "http://localhost:5000"; net/http just wants the host:port.
func stripScheme(url string) string {
	for _, prefix := range []string{"http://", "https://"} {
		if len(url) > len(prefix) && url[:len(prefix)] == prefix {
			return url[len(prefix):]
		}
	}
	return url
}
