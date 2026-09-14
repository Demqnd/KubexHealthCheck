// Package kubexmcp calls a single named tool on a Kubex MCP server, using
// a REST-issued bearer token (from internal/kubexauth) via the server's
// "/mcp-token" endpoint. Confirmed by live testing: unlike the plain
// "/mcp" endpoint (which requires a real MCP OAuth handshake and rejects
// a REST login token outright with an RFC 6750 "invalid_token" error),
// "/mcp-token" accepts that same REST token directly and doesn't need
// the initialize/notifications/initialized handshake first — a single
// "tools/call" request is enough.
package kubexmcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const protocolVersion = "2025-11-25"

type rpcRequest struct {
	JsonRpc string `json:"jsonrpc"`
	Id      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// CallTool calls toolName on the MCP server at mcpUrl (with "/mcp-token"
// appended) using token as the Bearer credential, and returns the
// concatenated text content of the tool result.
func CallTool(client *http.Client, mcpUrl, token, toolName string, arguments map[string]any) (string, error) {
	url := strings.TrimRight(mcpUrl, "/") + "/mcp-token"

	bodyBytes, err := json.Marshal(rpcRequest{
		JsonRpc: "2.0",
		Id:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read MCP response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("MCP server returned status %d: %s", resp.StatusCode, string(responseBytes))
	}

	jsonBytes, err := sseOrPlainJSON(resp.Header.Get("Content-Type"), responseBytes)
	if err != nil {
		return "", fmt.Errorf("failed to extract MCP response payload: %w", err)
	}

	var parsed rpcResponse
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		return "", fmt.Errorf("failed to decode MCP response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("status %d: %s", parsed.Error.Code, parsed.Error.Message)
	}

	var toolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(parsed.Result, &toolResult); err != nil {
		return "", fmt.Errorf("failed to parse tool result: %w", err)
	}

	var text strings.Builder
	for _, block := range toolResult.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	if toolResult.IsError {
		return "", fmt.Errorf("MCP tool %q returned an error: %s", toolName, text.String())
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("MCP tool %q returned no text content", toolName)
	}

	return text.String(), nil
}

// sseOrPlainJSON returns the JSON-RPC payload from an MCP response body.
// Per the Streamable HTTP transport spec, a server may answer a POST
// either as plain "application/json" or as Server-Sent Events — one
// "message" event per response, shaped like "event: message\ndata:
// {...}\n\n". Kubex's MCP server uses the SSE form.
func sseOrPlainJSON(contentType string, body []byte) ([]byte, error) {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return body, nil
	}

	var lastData, current []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if len(current) > 0 {
				lastData = current
				current = nil
			}
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			current = append(current, strings.TrimPrefix(data, " "))
		}
	}
	if len(current) > 0 {
		lastData = current
	}

	if len(lastData) == 0 {
		return nil, fmt.Errorf("no \"data:\" field found in SSE response")
	}
	return []byte(strings.Join(lastData, "\n")), nil
}
