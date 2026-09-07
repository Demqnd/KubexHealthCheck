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
	"net/url"
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

	// "bedrock <url> <skill> [instruction]" is the Bedrock equivalent of
	// the plain "<url> <skill>" Anthropic path below — Bedrock has no MCP
	// connector, so this routes through callBedrockWithMcpTool instead of
	// callClaude, using the same shared KubexMcpSettings token.
	bedrockPrefixPattern = regexp.MustCompile(`(?i)^bedrock\s+`)
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
		apiKey, model, err := s.anthropicCreds()
		if err != nil {
			return "", err
		}
		skillWord, instruction := splitFirstWord(fleetRest)
		return s.RunFleet(apiKey, model, skillWord, instruction)
	}

	// "bedrock <url> <skill> [instruction]" — the Bedrock equivalent of
	// the plain single-URL Anthropic path just below. Checked first since
	// the URL-only branch below would otherwise treat the leading
	// "bedrock" word as free-form instruction text rather than a routing
	// prefix.
	if bedrockRest := bedrockPrefixPattern.ReplaceAllString(content, ""); bedrockRest != content {
		// "bedrock fleet <skillword> [instruction]" — the Bedrock
		// equivalent of the plain "fleet" path above, checked first for
		// the same reason: no URL to look for at all.
		if fleetRest := fleetPrefixPattern.ReplaceAllString(bedrockRest, ""); fleetRest != bedrockRest {
			skillWord, instruction := splitFirstWord(fleetRest)
			return s.RunFleetBedrock(skillWord, instruction)
		}

		if loc := mcpUrlPattern.FindStringIndex(bedrockRest); loc != nil {
			mcpServerUrl := bedrockRest[loc[0]:loc[1]]
			instruction := strings.TrimSpace(bedrockRest[:loc[0]] + bedrockRest[loc[1]:])
			mcpToken := s.cfg.KubexMcpSettings.AuthorizationToken

			mcpSkill, mcpRest := s.resolveSkill(instruction)
			if mcpSkill != nil {
				mcpSkillInput := buildDateContext() + buildMcpContext(mcpServerUrl) + orDefault(mcpRest, "Run this skill.")
				return s.callBedrockWithMcpTool(mcpSkill.Instructions, mcpSkillInput, mcpServerUrl, mcpToken, requiredMcpToolName)
			}

			// No recognized skill word after the URL — default to
			// skills/kubex-health-check, mirroring the Anthropic path.
			defaultInstruction := buildMcpContext(mcpServerUrl) + orDefault(instruction, "Check the fleet's health.")
			if defaultSkill := s.skillRegistry.Find("kubex-health-check"); defaultSkill != nil {
				return s.callBedrockWithMcpTool(
					buildDateContext()+defaultSkill.Instructions, defaultInstruction, mcpServerUrl, mcpToken, requiredMcpToolName)
			}

			return s.callBedrockWithMcpTool(fallbackKubexMcpSystemPrompt, defaultInstruction, mcpServerUrl, mcpToken, requiredMcpToolName)
		}

		return "", fmt.Errorf(
			"a \"bedrock\" command needs either \"fleet <skill>\" or an MCP server URL, " +
				"e.g. \"bedrock https://sandbox-mcp.kubex.ai/... kubex-health-check\"")
	}

	if loc := mcpUrlPattern.FindStringIndex(content); loc != nil {
		apiKey, model, err := s.anthropicCreds()
		if err != nil {
			return "", err
		}
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
	//
	// Both branches here go through Bedrock, not Anthropic directly —
	// neither one attaches an MCP server, so there's nothing that needs
	// the MCP connector feature Bedrock doesn't support. A skill's own
	// "<!-- model:... -->" override (an Anthropic model name) doesn't
	// apply on this path, since Bedrock uses a completely different
	// model-ID namespace — BedrockSettings:ModelId is used for every
	// call here instead.
	skill, rest := s.resolveSkill(content)
	if skill != nil && skill.GenericallyDispatchable {
		skillInput := buildDateContext() + orDefault(rest, "Run this skill.")
		return s.callBedrock(skill.Instructions, skillInput)
	}

	return s.callBedrock(askSystemPrompt, content)
}

// RunFleet runs skillWord against every customer in customers.csv, each
// signed in with its own username/password (unlike the single shared
// KubexMcpSettings token used by the single-URL command path), and
// combines every customer's one-line answer into a single message.
//
// No MCP server is attached for any fleet customer, on either this path
// or RunFleetBedrock — see runFleet's doc comment for why.
//
// This is the extension point for the "feed all the info back into
// Claude for a final combined output" idea: right now the per-customer
// answers are just joined line by line, but that join step is exactly
// where a second Claude call synthesizing all of them into one polished
// report would slot in later, without touching how the fan-out itself
// works.
func (s *Service) RunFleet(apiKey, model, skillWord, instruction string) (string, error) {
	return s.runFleet(skillWord, instruction, func(skill *skills.Skill, input string) (string, error) {
		return s.callClaude(apiKey, resolveModel(skill, model), skill.Instructions, input, "", "")
	})
}

// RunFleetBedrock is RunFleet's Bedrock equivalent: same customers.csv
// fan-out and same pre-fetched-data approach, just calling Bedrock
// (plain, no tool-use loop needed — see runFleet) instead of Anthropic.
// No apiKey/model params — like every other Bedrock path,
// BedrockSettings supplies the model, and a skill's own model override
// doesn't apply here (Bedrock uses a different model-ID namespace).
func (s *Service) RunFleetBedrock(skillWord, instruction string) (string, error) {
	return s.runFleet(skillWord, instruction, func(skill *skills.Skill, input string) (string, error) {
		return s.callBedrock(skill.Instructions, input)
	})
}

// runFleet resolves skillWord, loads customers.csv, and fans call out
// across every customer (bounded by fleetConcurrency), joining each
// customer's one-line answer (or "FAILED - <err>") into a single report.
//
// Each customer's username/password signs in via kubexauth to get a
// Kubex REST API token — confirmed (both by live testing and Kubex's
// own MCP docs) that this token is NOT accepted by the MCP server,
// which requires its own separately-authorized OAuth credential. So
// instead of attaching an MCP server, this fetches the customer's
// cluster data directly via Kubex's REST API (kubexauth.FetchClusters)
// and embeds it as context before calling call — no MCP connector, and
// for Bedrock, no tool-use loop either, since the data's already in
// hand before the model is ever called.
//
// call receives the resolved skill (for skill.Instructions and, for the
// Anthropic path, its model override) plus the fully-built input text —
// RunFleet and RunFleetBedrock differ only in what call does with them.
func (s *Service) runFleet(skillWord, instruction string, call func(skill *skills.Skill, input string) (string, error)) (string, error) {
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
			clustersJson, err := kubexauth.FetchClusters(s.httpClient, authUrl, token)
			if err != nil {
				results[i] = outcome{name: c.Name, err: fmt.Errorf("failed to fetch cluster data: %w", err)}
				return
			}

			input := dateContext + buildClusterDataContext(c.Name, clustersJson) + orDefault(instruction, "Run this skill.")
			text, err := call(skill, input)
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

// kubexAuthUrl turns an MCP URL like "https://sandboxuat-mcp.kubex.ai/mcp"
// into the plain REST host its /api/v2/authorize login endpoint lives on
// ("https://sandboxuat.kubex.ai") — a different host than the MCP URL
// itself, confirmed by testing both directly: the MCP host enforces
// OAuth on every path, while the plain host (no "-mcp") serves the
// username/password login endpoint internal/kubexauth calls.
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

// anthropicCreds is only needed by the branches of RunCommand that still
// call Anthropic directly (fleet, single-URL MCP commands) — the plain
// Bedrock branches don't touch ClaudeApiSettings at all, so this check
// must not run unconditionally for every command.
func (s *Service) anthropicCreds() (apiKey, model string, err error) {
	apiKey = s.cfg.ClaudeApiSettings.ApiKey
	if strings.TrimSpace(apiKey) == "" {
		return "", "", fmt.Errorf("ClaudeApiSettings:ApiKey is not configured")
	}

	model = s.cfg.ClaudeApiSettings.Model
	if strings.TrimSpace(model) == "" {
		model = defaultModel
	}
	return apiKey, model, nil
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

// buildClusterDataContext embeds cluster data already fetched via
// Kubex's REST API (kubexauth.FetchClusters) for one fleet customer, in
// place of attaching an MCP server (see runFleet's doc comment for why
// fleet customers can't use one at all). The REST response's field
// names differ from the "kubex-cluster-connections" MCP tool's — and,
// importantly, it has no live connector "status" field at all — so both
// are called out explicitly here to keep the skill's health-check logic
// from silently degrading.
func buildClusterDataContext(customerName, clustersJson string) string {
	return fmt.Sprintf(
		"[Context: here is the real, current cluster connection data for %s, fetched directly from Kubex's REST "+
			"API (GET /kubernetes/clusters) rather than the kubex-cluster-connections MCP tool. Field names differ "+
			"slightly (\"cluster\" not \"clusterName\", \"lastCollectionTime\" not \"lastDataCollectionTime\", "+
			"\"kubexAgentVersion\" not \"forwarderVersion\"), and there is no live \"status\" field in this data at "+
			"all — treat a cluster that hasn't collected data recently as needing attention instead of looking for "+
			"a status value. Use ONLY this data to answer; do not ask which client to use or attempt to call any "+
			"tools:]\n\n%s\n\n",
		customerName, clustersJson)
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
