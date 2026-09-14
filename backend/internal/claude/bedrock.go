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
)

type bedrockContentBlock struct {
	Text string `json:"text,omitempty"`
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

// callBedrock sends a system prompt + user message to AWS Bedrock's
// Converse API and returns the model's text answer.
func (s *Service) callBedrock(systemPrompt, userContent string) (string, error) {
	log.Printf("Bedrock Converse call")
	resp, err := s.bedrockConverse(
		[]bedrockMessage{{Role: "user", Content: []bedrockContentBlock{{Text: userContent}}}},
		systemPrompt)
	if err != nil {
		return "", err
	}
	return firstBedrockText(resp)
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

func (s *Service) bedrockConverse(messages []bedrockMessage, systemPrompt string) (*bedrockConverseResponse, error) {
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
