// Package kubexmcp is a minimal MCP client — not a general-purpose SDK,
// just enough to call one named tool on a Streamable HTTP MCP server.
// This exists because AWS Bedrock has no equivalent to Anthropic's MCP
// connector (which does this same job automatically): when Claude is
// called via Bedrock and decides to use an MCP-backed tool, something
// has to actually speak the MCP protocol to fetch the result, and that's
// this package's only job.
package kubexmcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const protocolVersion = "2025-11-25"

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{httpClient: &http.Client{Timeout: 30 * time.Second}}
}

type rpcRequest struct {
	JsonRpc string `json:"jsonrpc"`
	Id      int    `json:"id,omitempty"`
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

// CallTool performs the MCP handshake (initialize, then the
// notifications/initialized notification, capturing any Mcp-Session-Id
// the server assigns along the way) and then calls toolName with
// arguments, against url using token as the Bearer credential. Returns
// the concatenated text content of the tool result.
func (c *Client) CallTool(url, token, toolName string, arguments map[string]any) (string, error) {
	sessionId, err := c.initialize(url, token)
	if err != nil {
		return "", fmt.Errorf("MCP initialize failed: %w", err)
	}

	result, err := c.call(url, token, sessionId, rpcRequest{
		JsonRpc: "2.0",
		Id:      2,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	})
	if err != nil {
		return "", fmt.Errorf("MCP tools/call failed: %w", err)
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse tool result: %w", err)
	}

	var text strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	if parsed.IsError {
		return "", fmt.Errorf("MCP tool %q returned an error: %s", toolName, text.String())
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("MCP tool %q returned no text content", toolName)
	}

	return text.String(), nil
}

// initialize performs the initialize handshake and the
// notifications/initialized notification, returning whatever
// Mcp-Session-Id the server assigned (empty if none).
func (c *Client) initialize(url, token string) (string, error) {
	initReq := rpcRequest{
		JsonRpc: "2.0",
		Id:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "kubexhealthcheck",
				"version": "1.0.0",
			},
		},
	}

	resp, sessionId, err := c.post(url, token, "", initReq)
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("status %d: %s", resp.Error.Code, resp.Error.Message)
	}

	// Fire-and-forget notification — no "id" field means no response is
	// expected, per JSON-RPC 2.0. Sent on the same session so the server
	// can complete its side of the handshake before tools/call.
	notifyReq := rpcRequest{JsonRpc: "2.0", Method: "notifications/initialized"}
	if _, _, err := c.post(url, token, sessionId, notifyReq); err != nil {
		return "", err
	}

	return sessionId, nil
}

func (c *Client) call(url, token, sessionId string, req rpcRequest) (json.RawMessage, error) {
	resp, _, err := c.post(url, token, sessionId, req)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("status %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// post sends one JSON-RPC message and returns the parsed response (if
// any — a notification has no response body to parse) along with
// whatever Mcp-Session-Id the server returned.
func (c *Client) post(url, token, sessionId string, body rpcRequest) (*rpcResponse, string, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("MCP-Protocol-Version", protocolVersion)
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionId != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionId)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	respSessionId := resp.Header.Get("Mcp-Session-Id")
	if respSessionId == "" {
		respSessionId = sessionId
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, respSessionId, fmt.Errorf("MCP server returned status %d", resp.StatusCode)
	}

	// A notification (no "id") has no response body to decode.
	if body.Id == 0 {
		return &rpcResponse{}, respSessionId, nil
	}

	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, respSessionId, fmt.Errorf("failed to read MCP response: %w", err)
	}

	jsonBytes, err := sseOrPlainJSON(resp.Header.Get("Content-Type"), responseBytes)
	if err != nil {
		return nil, respSessionId, fmt.Errorf("failed to extract MCP response payload: %w", err)
	}

	var parsed rpcResponse
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		return nil, respSessionId, fmt.Errorf("failed to decode MCP response: %w", err)
	}

	return &parsed, respSessionId, nil
}

// sseOrPlainJSON returns the JSON-RPC payload from an MCP response body.
// Per the Streamable HTTP transport spec, a server may answer a POST
// either as plain "application/json" or as Server-Sent Events — one
// "message" event per response, shaped like "event: message\ndata:
// {...}\n\n". Kubex's MCP server uses the SSE form; decoding that framing
// directly as JSON fails, since "event: message\ndata: " isn't JSON at
// all until the "data:" field is pulled back out.
func sseOrPlainJSON(contentType string, body []byte) ([]byte, error) {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		return body, nil
	}

	// A POST response carries exactly one JSON-RPC message for the
	// request it answers, so the last complete "data:" block in the
	// stream (multiple "data:" lines within one event are joined per the
	// SSE spec) is the one to use.
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
