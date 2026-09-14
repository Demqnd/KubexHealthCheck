// Package claude runs a typed command against AWS Bedrock: either a
// plain message, or the fleet health check across every customer in
// customers.csv.
package claude

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"kubexhealthcheck/internal/config"
	"kubexhealthcheck/internal/customers"
	"kubexhealthcheck/internal/kubexauth"
	"kubexhealthcheck/internal/kubexmcp"
	"kubexhealthcheck/internal/skills"
)

// How many customers' calls run at once during a fleet health check —
// bounded so a 40-customer run doesn't fire 40 simultaneous requests at
// once.
const fleetConcurrency = 5

const (
	healthCheckSkillName = "kubex-health-check"

	// The Kubex MCP tool the health check skill needs. Called via
	// internal/kubexmcp's "/mcp-token" endpoint, using a REST login
	// token from internal/kubexauth.
	requiredMcpToolName = "kubex-cluster-connections"

	askSystemPrompt = "You are a helpful assistant. Answer the user's question clearly and concisely, in plain text " +
		"suitable for posting in a Teams message. Do not use markdown formatting (no headers, bullets, or bold)."
)

// Matches "health check", "healthcheck", "run health check", etc. —
// the one fleet-report trigger this service recognizes.
var healthCheckPattern = regexp.MustCompile(`(?i)health.?check`)

type Service struct {
	cfg           *config.Config
	skillRegistry *skills.Registry
	customersFile string
	httpClient    *http.Client
	kubexAuth     *kubexauth.Cache
}

func NewService(cfg *config.Config, skillRegistry *skills.Registry, customersFile string) *Service {
	return &Service{
		cfg:           cfg,
		skillRegistry: skillRegistry,
		customersFile: customersFile,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		kubexAuth:     kubexauth.NewCache(),
	}
}

// RunCommand runs whatever was typed into the frontend: a "health
// check" command runs the fleet report across every customer in
// customers.csv; anything else is either dispatched to a matching
// skill by name (e.g. "onthisday") or sent to Bedrock as a plain
// free-form message.
func (s *Service) RunCommand(command string) (string, error) {
	content := strings.TrimSpace(command)

	if healthCheckPattern.MatchString(content) {
		return s.RunFleetHealthCheck()
	}

	skill, rest := s.resolveSkill(content)
	if skill != nil {
		skillInput := buildDateContext() + orDefault(rest, "Run this skill.")
		return s.callBedrock(skill.Instructions, skillInput)
	}

	return s.callBedrock(askSystemPrompt, content)
}

// RunFleetHealthCheck runs the kubex-health-check skill against every
// customer in customers.csv, combining every customer's one-line answer
// into a single message.
//
// Each customer signs in with its own username/password (via
// internal/kubexauth) to get a Kubex REST login token, then that token
// is used to call the real kubex-cluster-connections MCP tool (via
// internal/kubexmcp's "/mcp-token" endpoint — confirmed by live testing
// to accept this kind of token, unlike the plain "/mcp" endpoint). The
// tool's real result is handed to Bedrock as context alongside the
// skill's instructions, in a single call — no tool-use loop needed,
// since the data's already in hand before Bedrock is ever called.
func (s *Service) RunFleetHealthCheck() (string, error) {
	skill := s.skillRegistry.Find(healthCheckSkillName)
	if skill == nil {
		return "", fmt.Errorf("no skill named %q is installed", healthCheckSkillName)
	}

	list, err := customers.Load(s.customersFile)
	if err != nil {
		return "", fmt.Errorf("failed to load %s: %w", s.customersFile, err)
	}
	if len(list) == 0 {
		return "", fmt.Errorf("no customers configured in %s", s.customersFile)
	}

	dateContext := buildDateContext()

	type outcome struct {
		name string
		text string
		err  error
	}

	results := make([]outcome, len(list))
	sem := make(chan struct{}, fleetConcurrency)
	var wg sync.WaitGroup

	for i, customer := range list {
		wg.Add(1)
		go func(i int, c customers.Customer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			authUrl, err := kubexAuthUrl(c.McpUrl)
			if err != nil {
				results[i] = outcome{name: c.Name, err: err}
				return
			}
			token, err := s.kubexAuth.Token(authUrl, c.Username, c.Password)
			if err != nil {
				results[i] = outcome{name: c.Name, err: fmt.Errorf("sign-in failed: %w", err)}
				return
			}
			toolText, err := kubexmcp.CallTool(s.httpClient, c.McpUrl, token, requiredMcpToolName, map[string]any{})
			if err != nil {
				results[i] = outcome{name: c.Name, err: fmt.Errorf("failed to call %s: %w", requiredMcpToolName, err)}
				return
			}

			input := dateContext + buildMcpToolContext(c.Name, toolText) + "Run this skill."
			text, err := s.callBedrock(skill.Instructions, input)
			results[i] = outcome{name: c.Name, text: strings.TrimSpace(text), err: err}
		}(i, customer)
	}
	wg.Wait()

	lines := make([]string, len(results))
	failures := 0
	for i, r := range results {
		if r.err != nil {
			failures++
			lines[i] = fmt.Sprintf("%s: FAILED - %s", r.name, r.err.Error())
		} else {
			lines[i] = fmt.Sprintf("%s: %s", r.name, r.text)
		}
	}

	summary := strings.Join(lines, "\n")
	if failures > 0 {
		summary = fmt.Sprintf("Fleet report: %d of %d customers failed.\n\n%s", failures, len(results), summary)
	}
	return summary, nil
}

func splitFirstWord(content string) (first string, rest string) {
	if idx := strings.IndexByte(content, ' '); idx >= 0 {
		return content[:idx], strings.TrimSpace(content[idx+1:])
	}
	return content, ""
}

func (s *Service) resolveSkill(content string) (*skills.Skill, string) {
	firstWord, rest := splitFirstWord(content)
	return s.skillRegistry.Find(firstWord), rest
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// Claude has no clock of its own — skills that need "today" (like
// onthisday) get it supplied here rather than guessing from training
// data.
func buildDateContext() string {
	now := time.Now().UTC()
	if eastern, err := time.LoadLocation("America/New_York"); err == nil {
		now = now.In(eastern)
	}
	return fmt.Sprintf("[Context: today's date is %s (%s), US Eastern.]\n\n", now.Format("2006-01-02"), now.Format("Monday"))
}

// buildMcpToolContext embeds the real kubex-cluster-connections tool
// result for one fleet customer, telling the model to use it directly
// instead of trying to resolve a connector or call a tool itself.
func buildMcpToolContext(customerName, toolResultJson string) string {
	return fmt.Sprintf(
		"[Context: here is the real, current output of the %s tool for %s — that's the client this run is "+
			"for. Do not ask which client to use, and don't try to call any tools yourself — just use this "+
			"data:]\n\n%s\n\n",
		requiredMcpToolName, customerName, toolResultJson)
}

// kubexAuthUrl turns an MCP URL like "https://sandboxuat-mcp.kubex.ai"
// into the plain REST host its /api/v2/authorize login endpoint lives
// on ("https://sandboxuat.kubex.ai") — a different host than the MCP
// URL itself.
func kubexAuthUrl(mcpUrl string) (string, error) {
	parsed, err := url.Parse(mcpUrl)
	if err != nil {
		return "", fmt.Errorf("invalid MCP URL %q: %w", mcpUrl, err)
	}
	if !strings.Contains(parsed.Host, "-mcp") {
		return "", fmt.Errorf("MCP URL %q does not look like a Kubex MCP host (expected \"-mcp\" in the hostname)", mcpUrl)
	}
	parsed.Host = strings.Replace(parsed.Host, "-mcp", "", 1)
	parsed.Path = ""
	return parsed.String(), nil
}
