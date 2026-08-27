// Package claude talks to the Anthropic Messages API, including
// dispatching commands to installed skills and attaching a Kubex MCP
// server when a command names one.
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"kubexhealthcheck/internal/config"
	"kubexhealthcheck/internal/customers"
	"kubexhealthcheck/internal/kubexauth"
	"kubexhealthcheck/internal/skills"
)

// How many customers' Claude calls run at once during a fleet report —
// bounded so a 40-customer run doesn't slam Anthropic's rate limits with
// 40 simultaneous requests.
const fleetConcurrency = 5

const (
	apiUrl           = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	mcpBetaHeader    = "mcp-client-2025-11-20"
	defaultModel     = "claude-opus-5"
	mcpServerName    = "kubex-mcp"

	// The only Kubex MCP tool this service actually calls right now. Used
	// to allowlist the mcp_toolset below — see the comment on "tools" in
	// callClaude for why that matters.
	requiredMcpToolName = "kubex-cluster-connections"

	askSystemPrompt = "You are a helpful assistant. Answer the user's question clearly and concisely, in plain text " +
		"suitable for posting in a Teams message. Do not use markdown formatting (no headers, bullets, or bold)."

	// Last-resort fallback only: used if an MCP command can't find ANY
	// loaded skill to run — not even skills/kubex-health-check — which
	// normally only happens if the skills/ folder is missing or
	// misconfigured. In normal operation, the actual
	// skills/kubex-health-check/SKILL.md content is what runs; this
	// constant is not that file and will drift from it — it's a safety
	// net, not a copy to keep in sync.
	fallbackKubexMcpSystemPrompt = "You are an SRE assistant with access to a connected Kubex MCP server's tools. Call the Kubex " +
		"cluster-connections tool to get per-cluster health data, and produce a short plain-text summary " +
		"covering cluster count, status, 24-hour data freshness, and forwarder/Prometheus version drift. " +
		"Keep it to one tight paragraph, no markdown formatting - it will be posted directly as a Teams message."
)

var (
	mcpUrlPattern = regexp.MustCompile(`https?://\S+`)

	// Strips a leading "@KubexAI" (or any other @-mention) so the word
	// right after it is what gets checked against the skill registry —
	// Teams message text arrives with the mention still in it.
	leadingMentionPattern = regexp.MustCompile(`^@\S+\s*`)

	// "fleet <skillword> [instruction]" runs that skill against every
	// customer in customers.csv (each with its own MCP URL + token) and
	// combines all their answers into one message, instead of the usual
	// single-URL-per-command path.
	fleetPrefixPattern = regexp.MustCompile(`(?i)^fleet\s+`)
)

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

func (s *Service) Ask(apiKey, question string) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("an Anthropic API key is required")
	}

	model := s.cfg.ClaudeApiSettings.Model
	if strings.TrimSpace(model) == "" {
		model = defaultModel
	}
	return s.callClaude(apiKey, model, askSystemPrompt, question, "", "")
}

func (s *Service) RunCommand(command string) (string, error) {
	apiKey := s.cfg.ClaudeApiSettings.ApiKey
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("ClaudeApiSettings:ApiKey is not configured")
	}

	model := s.cfg.ClaudeApiSettings.Model
	if strings.TrimSpace(model) == "" {
		model = defaultModel
	}

	// Drop a leading "@KubexAI" (or any @-mention) before anything below
	// looks at the command's first word — Teams delivers the mention as
	// literal text, and it would otherwise shadow a skill word.
	content := strings.TrimSpace(leadingMentionPattern.ReplaceAllString(strings.TrimSpace(command), ""))
	if content == "" {
		content = strings.TrimSpace(command)
	}

	// "fleet <skillword> [instruction]" — run that skill against every
	// customer in customers.csv, each with its own MCP URL + token, and
	// combine all their answers into one message. Checked before the
	// single-URL path below since a fleet command has no URL in it at
	// all — the URLs come from customers.csv instead.
	if fleetRest := fleetPrefixPattern.ReplaceAllString(content, ""); fleetRest != content {
		skillWord, instruction := splitFirstWord(fleetRest)
		return s.RunFleet(apiKey, model, skillWord, instruction)
	}

	if loc := mcpUrlPattern.FindStringIndex(content); loc != nil {
		mcpServerUrl := content[loc[0]:loc[1]]
		instruction := strings.TrimSpace(content[:loc[0]] + content[loc[1]:])
		mcpToken := s.cfg.KubexMcpSettings.AuthorizationToken

		// Does the word right after the URL name a loaded skill (e.g.
		// "kubex-health-check", "fedex-cost-report")? This ignores
		// GenericallyDispatchable on purpose: an MCP server is already
		// attached below, so a skill marked dispatch:false for the
		// no-MCP path is exactly usable here.
		mcpSkill, mcpRest := s.resolveSkill(instruction)
		if mcpSkill != nil {
			mcpSkillInput := buildDateContext() + buildMcpContext(mcpServerUrl) + orDefault(mcpRest, "Run this skill.")
			return s.callClaude(apiKey, resolveModel(mcpSkill, model), mcpSkill.Instructions, mcpSkillInput, mcpServerUrl, mcpToken)
		}

		// No recognized skill word after the URL (e.g. "@KubexAI <url>
		// check cluster status") — default to skills/kubex-health-check,
		// the same behavior this had before skill words existed here.
		defaultInstruction := buildMcpContext(mcpServerUrl) + orDefault(instruction, "Check the fleet's health.")
		if defaultSkill := s.skillRegistry.Find("kubex-health-check"); defaultSkill != nil {
			return s.callClaude(
				apiKey, resolveModel(defaultSkill, model), buildDateContext()+defaultSkill.Instructions,
				defaultInstruction, mcpServerUrl, mcpToken)
		}

		// skills/kubex-health-check/SKILL.md itself is missing or
		// unreadable (misconfigured SkillsDirectory, bad deploy, etc.) —
		// don't fail the request outright, answer with the fallback.
		return s.callClaude(apiKey, model, fallbackKubexMcpSystemPrompt, defaultInstruction, mcpServerUrl, mcpToken)
	}

	// No MCP URL — see if the first word names an installed skill
	// (skills/<name>/SKILL.md). Skills marked dispatch:false are excluded
	// here since this path has no way to attach an MCP server for them.
	// Otherwise fall back to a plain free-form question.
	skill, rest := s.resolveSkill(content)
	if skill != nil && skill.GenericallyDispatchable {
		skillInput := buildDateContext() + orDefault(rest, "Run this skill.")
		return s.callClaude(apiKey, resolveModel(skill, model), skill.Instructions, skillInput, "", "")
	}

	return s.callClaude(apiKey, model, askSystemPrompt, content, "", "")
}

// RunFleet runs skillWord against every customer in customers.csv, each
// with its own MCP URL and auth token (unlike the single shared
// KubexMcpSettings token used by the single-URL command path), and
// combines every customer's one-line answer into a single message.
//
// The token here is read directly from customers.csv (manually
// obtained, e.g. via the MCP Inspector) rather than signed in for
// automatically — internal/kubexauth has a username/password sign-in
// path ready to swap in here once Kubex account setup (the
// "API-enabled" flag) allows testing whether it actually works for MCP
// auth, not just Kubex's plain REST API.
//
// This is the extension point for the "feed all the info back into
// Claude for a final combined output" idea: right now the per-customer
// answers are just joined line by line, but that join step is exactly
// where a second Claude call synthesizing all of them into one polished
// report would slot in later, without touching how the fan-out itself
// works.
func (s *Service) RunFleet(apiKey, model, skillWord, instruction string) (string, error) {
	skill := s.skillRegistry.Find(skillWord)
	if skill == nil {
		return "", fmt.Errorf("no skill named %q is installed", skillWord)
	}

	list, err := customers.Load(s.customersFile)
	if err != nil {
		return "", fmt.Errorf("failed to load %s: %w", s.customersFile, err)
	}
	if len(list) == 0 {
		return "", fmt.Errorf("no customers configured in %s", s.customersFile)
	}

	skillModel := resolveModel(skill, model)
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

			input := dateContext + buildMcpContext(c.McpUrl) + orDefault(instruction, "Run this skill.")
			text, err := s.callClaude(apiKey, skillModel, skill.Instructions, input, c.McpUrl, c.AuthorizationToken)
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

// A skill's own "<!-- model:... -->" marker wins over the caller's model,
// so one narrow/cheap skill can run on a cheaper model without changing
// what every other command uses.
func resolveModel(skill *skills.Skill, fallbackModel string) string {
	if strings.TrimSpace(skill.Model) == "" {
		return fallbackModel
	}
	return skill.Model
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

// The MCP server URL never reaches Claude any other way — it's stripped
// out of the command text and only ever appears in the request's
// mcp_servers config, which Claude's own context doesn't surface as
// readable text. Without this line, "no client identifier was mentioned"
// and "a client was already specified via URL" are indistinguishable to
// Claude, and a skill written to ask when nothing was specified has no
// way to tell them apart — it'll default to asking, every time, even
// though a URL was given.
func buildMcpContext(mcpServerUrl string) string {
	return fmt.Sprintf(
		"[Context: this request already has a Kubex MCP server attached, for %s — "+
			"that's the client this run is for. Do not ask which client to use, and skip any "+
			"connector-list/resolution step — just use the MCP tools already available to you.]\n\n",
		mcpServerUrl)
}

func (s *Service) callClaude(apiKey, model, systemPrompt, userContent, mcpServerUrl, mcpToken string) (string, error) {
	maxTokens := 1024
	if mcpServerUrl != "" {
		maxTokens = 4096
	}

	requestBody := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"thinking":   map[string]any{"type": "disabled"},
		"system":     systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": userContent},
		},
	}

	// Haiku models 400 on output_config.effort ("This model does not
	// support the effort parameter") — only Opus/Sonnet-family models
	// accept it, so skip it for anything Haiku (e.g. a skill's
	// "<!-- model:... -->" override).
	if !strings.Contains(strings.ToLower(model), "haiku") {
		requestBody["output_config"] = map[string]any{"effort": "low"}
	}

	if mcpServerUrl != "" {
		requestBody["mcp_servers"] = []map[string]any{
			{
				"type":                "url",
				"url":                 mcpServerUrl,
				"name":                mcpServerName,
				"authorization_token": mcpToken,
			},
		}
		requestBody["tools"] = []map[string]any{
			{
				"type":            "mcp_toolset",
				"mcp_server_name": mcpServerName,
				// Allowlist: without this, Anthropic loads the MCP
				// server's ENTIRE tool catalog into every request
				// (Kubex's server exposes ~28 tools with verbose
				// descriptions - tens of thousands of input tokens
				// billed on every call, even though this skill only
				// ever calls one of them). Add a name here if a future
				// skill needs a different Kubex tool.
				"default_config": map[string]any{"enabled": false},
				"configs": map[string]any{
					requiredMcpToolName: map[string]any{"enabled": true},
				},
			},
		}
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, apiUrl, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	if mcpServerUrl != "" {
		req.Header.Set("anthropic-beta", mcpBetaHeader)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	responseJson, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Claude API call failed with status %d: %s", resp.StatusCode, string(responseJson))
	}

	var parsed struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(responseJson, &parsed); err != nil {
		return "", err
	}

	if parsed.StopReason == "refusal" {
		return "", fmt.Errorf("Claude declined to respond to this request")
	}

	// Take the LAST text block, not the first: when Claude uses an MCP
	// tool, the content array can contain preamble text before the tool
	// call and the real answer after the tool result.
	text := ""
	for _, block := range parsed.Content {
		if block.Type == "text" {
			text = block.Text
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("Claude API response did not contain a text block")
	}

	return text, nil
}
