package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// callBedrock is the AWS Bedrock equivalent of callClaude, for plain
// (no MCP server attached) prompts only. Bedrock's Converse API doesn't
// support Anthropic's MCP connector at all, so any command that attaches
// a Kubex MCP server (a single-URL command, or "fleet") still goes
// through callClaude/Anthropic's direct API — this is only wired into
// RunCommand's non-MCP branches (plain skills like onthisday, and the
// generic free-form question fallback).
func (s *Service) callBedrock(systemPrompt, userContent string) (string, error) {
	region := s.cfg.BedrockSettings.Region
	modelId := s.cfg.BedrockSettings.ModelId
	apiKey := s.cfg.BedrockSettings.ApiKey

	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("BedrockSettings:ApiKey is not configured")
	}
	if strings.TrimSpace(region) == "" {
		return "", fmt.Errorf("BedrockSettings:Region is not configured")
	}
	if strings.TrimSpace(modelId) == "" {
		return "", fmt.Errorf("BedrockSettings:ModelId is not configured")
	}

	requestBody := map[string]any{
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": []map[string]any{{"text": userContent}},
			},
		},
		"system": []map[string]any{{"text": systemPrompt}},
		"inferenceConfig": map[string]any{
			"maxTokens": 1024,
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	// Model IDs (or cross-region inference profile IDs) can contain
	// characters like ':' — escape the path segment rather than
	// concatenating it raw into the URL.
	bedrockUrl := fmt.Sprintf(
		"https://bedrock-runtime.%s.amazonaws.com/model/%s/converse",
		region, url.PathEscape(modelId))

	req, err := http.NewRequest(http.MethodPost, bedrockUrl, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
		return "", fmt.Errorf("Bedrock Converse call failed with status %d: %s", resp.StatusCode, string(responseJson))
	}

	var parsed struct {
		Output struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(responseJson, &parsed); err != nil {
		return "", err
	}

	var text strings.Builder
	for _, block := range parsed.Output.Message.Content {
		text.WriteString(block.Text)
	}

	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("Bedrock response did not contain a text block")
	}

	return text.String(), nil
}
