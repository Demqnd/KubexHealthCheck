package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Sender posts a plain-text message to a Teams incoming webhook as an
// Adaptive Card.
type Sender struct {
	client *http.Client
}

func NewSender() *Sender {
	return &Sender{client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Sender) Send(webhookUrl string, message string) error {
	parsed, err := url.ParseRequestURI(webhookUrl)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("the configured webhook URL is not a valid http/https URL")
	}

	payload := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]any{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.4",
					"body": []map[string]any{
						{
							"type": "TextBlock",
							"text": message,
							"wrap": true,
						},
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook call failed with status %d (%s)", resp.StatusCode, resp.Status)
	}
	return nil
}
