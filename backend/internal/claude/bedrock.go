package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"kubexhealthcheck/internal/kubexmcp"
)

// bedrockContentBlock covers the three content block shapes this file
// deals with: plain text, a tool-use request from the model, and a tool
// result we send back. Only the relevant fields get set/marshaled for
// any given block, via "omitempty".
type bedrockContentBlock struct {
	Text       string             `json:"text,omitempty"`
	ToolUse    *bedrockToolUse    `json:"toolUse,omitempty"`
	ToolResult *bedrockToolResult `json:"toolResult,omitempty"`
}

type bedrockToolUse struct {
	ToolUseId string         `json:"toolUseId"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

type bedrockToolResult struct {
	ToolUseId string           `json:"toolUseId"`
	Content   []map[string]any `json:"content"`
	Status    string           `json:"status,omitempty"`
}

type bedrockMessage struct {
	Role    string                `json:"role"`
	Content []bedrockContentBlock `json:"content"`
}

type bedrockConverseResponse struct {
	Output struct {
		Message bedrockMessage `json:"message"`
	} `json:"output"`
	StopReason string `json:"stopReason"`
}

// callBedrock is the AWS Bedrock equivalent of callClaude, for plain
// (no MCP server attached) prompts. Used by RunCommand's non-MCP
// branches (plain skills like onthisday, and the generic free-form
// question fallback).
func (s *Service) callBedrock(systemPrompt, userContent string) (string, error) {
	log.Printf("Bedrock Converse call (no tool)")
	resp, err := s.bedrockConverse(
		[]bedrockMessage{{Role: "user", Content: []bedrockContentBlock{{Text: userContent}}}},
		systemPrompt, nil)
	if err != nil {
		return "", err
	}
	return firstBedrockText(resp)
}

// callBedrockWithMcpTool is Bedrock's equivalent of callClaude's MCP
// path — Bedrock has no MCP connector, so this implements the
// client-side tool-use loop AWS documents for the Converse API: define
// the tool, let the model ask to call it, actually call it ourselves
// (via kubexmcp, since somebody has to speak MCP), and send the result
// back for the model to finish its answer with.
func (s *Service) callBedrockWithMcpTool(systemPrompt, userContent, mcpServerUrl, mcpToken, toolName string) (string, error) {
	toolConfig := map[string]any{
		"tools": []map[string]any{
			{
				"toolSpec": map[string]any{
					"name":        bedrockToolName(toolName),
					"description": "Get per-cluster Kubernetes health data for this Kubex client.",
					"inputSchema": map[string]any{
						"json": map[string]any{
							"type":       "object",
							"properties": map[string]any{},
						},
					},
				},
			},
		},
	}

	messages := []bedrockMessage{{Role: "user", Content: []bedrockContentBlock{{Text: userContent}}}}

	log.Printf("Bedrock Converse call 1/2 for %s (asking if %s is needed)", mcpServerUrl, toolName)
	resp, err := s.bedrockConverse(messages, systemPrompt, toolConfig)
	if err != nil {
		return "", err
	}

	if resp.StopReason != "tool_use" {
		log.Printf("Bedrock answered directly for %s, no tool call needed", mcpServerUrl)
		return firstBedrockText(resp)
	}

	var toolUse *bedrockToolUse
	for _, block := range resp.Output.Message.Content {
		if block.ToolUse != nil {
			toolUse = block.ToolUse
			break
		}
	}
	if toolUse == nil {
		return "", fmt.Errorf("Bedrock reported stopReason \"tool_use\" but no toolUse block was found")
	}

	mcpClient := kubexmcp.NewClient()
	toolText, toolErr := mcpClient.CallTool(mcpServerUrl, mcpToken, toolName, map[string]any{})

	toolResult := bedrockToolResult{ToolUseId: toolUse.ToolUseId}
	if toolErr != nil {
		toolResult.Content = []map[string]any{{"text": toolErr.Error()}}
		toolResult.Status = "error"
	} else {
		toolResult.Content = []map[string]any{{"text": toolText}}
	}

	messages = append(messages, resp.Output.Message)
	messages = append(messages, bedrockMessage{
		Role:    "user",
		Content: []bedrockContentBlock{{ToolResult: &toolResult}},
	})

	log.Printf("Bedrock Converse call 2/2 for %s (sending %s result back for final answer)", mcpServerUrl, toolName)
	finalResp, err := s.bedrockConverse(messages, systemPrompt, toolConfig)
	if err != nil {
		return "", err
	}
	return firstBedrockText(finalResp)
}

// bedrockToolName maps a Kubex MCP tool name (which contains hyphens,
// e.g. "kubex-cluster-connections") to a name Bedrock's toolSpec accepts
// — hyphens aren't valid there, so this uses underscores for the
// Bedrock-facing name while kubexmcp.CallTool still gets the real,
// unmodified MCP tool name.
func bedrockToolName(mcpToolName string) string {
	return strings.ReplaceAll(mcpToolName, "-", "_")
}

func firstBedrockText(resp *bedrockConverseResponse) (string, error) {
	var text strings.Builder
	for _, block := range resp.Output.Message.Content {
		text.WriteString(block.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("Bedrock response did not contain a text block")
	}
	return text.String(), nil
}

func (s *Service) bedrockConverse(messages []bedrockMessage, systemPrompt string, toolConfig map[string]any) (*bedrockConverseResponse, error) {
	region := s.cfg.BedrockSettings.Region
	modelId := s.cfg.BedrockSettings.ModelId
	apiKey := s.cfg.BedrockSettings.ApiKey

	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("BedrockSettings:ApiKey is not configured")
	}
	if strings.TrimSpace(region) == "" {
		return nil, fmt.Errorf("BedrockSettings:Region is not configured")
	}
	if strings.TrimSpace(modelId) == "" {
		return nil, fmt.Errorf("BedrockSettings:ModelId is not configured")
	}

	requestBody := map[string]any{
		"messages": messages,
		"system":   []map[string]any{{"text": systemPrompt}},
		"inferenceConfig": map[string]any{
			"maxTokens": 1024,
		},
	}
	if toolConfig != nil {
		requestBody["toolConfig"] = toolConfig
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	// Model IDs (or cross-region inference profile IDs) can contain
	// characters like ':' — escape the path segment rather than
	// concatenating it raw into the URL.
	bedrockUrl := fmt.Sprintf(
		"https://bedrock-runtime.%s.amazonaws.com/model/%s/converse",
		region, url.PathEscape(modelId))

	req, err := http.NewRequest(http.MethodPost, bedrockUrl, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	responseJson, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Bedrock Converse call failed with status %d: %s", resp.StatusCode, string(responseJson))
	}

	var parsed bedrockConverseResponse
	if err := json.Unmarshal(responseJson, &parsed); err != nil {
		return nil, err
	}

	return &parsed, nil
}
