// Package kubexauth signs in to Kubex with a username/password to get a
// bearer token, instead of relying on a manually-obtained MCP OAuth
// token that expires quickly. This calls Kubex's plain REST login
// endpoint (POST {url}/api/v2/authorize), the same one the old
// KubexHealthCheckService used before this project moved to MCP — it is
// NOT the MCP server's own OAuth 2.1 flow.
//
// Confirmed directly (both by live testing and by Kubex's own docs):
// this token is NOT accepted as a bearer token by the MCP server. MCP
// access requires its own separately-authorized OAuth credential
// ("explicitly authorized their LLM client application," refreshed
// daily, per Kubex's MCP docs) — a fundamentally different credential
// than this plain JWT login token. This token IS valid for Kubex's
// other REST endpoints, though (see FetchClusters below).
package kubexauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Kubex documents /authorize tokens as good for 60 minutes; refresh a
// little early so a request never lands right on the expiry edge.
const tokenLifetime = 55 * time.Minute

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Cache signs in once per (url, username) pair and reuses the token
// until it's about to expire, instead of re-authenticating on every
// call — important once a fleet run covers many customers.
type Cache struct {
	client *http.Client
	mu     sync.Mutex
	tokens map[string]cachedToken
}

func NewCache() *Cache {
	return &Cache{
		client: &http.Client{Timeout: 15 * time.Second},
		tokens: make(map[string]cachedToken),
	}
}

// Token returns a cached bearer token for (url, username), signing in
// fresh if there's no cached token yet or the cached one is near expiry.
func (c *Cache) Token(url, username, password string) (string, error) {
	key := url + "|" + username

	c.mu.Lock()
	cached, ok := c.tokens[key]
	c.mu.Unlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.token, nil
	}

	token, err := c.login(url, username, password)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.tokens[key] = cachedToken{token: token, expiresAt: time.Now().Add(tokenLifetime)}
	c.mu.Unlock()

	return token, nil
}

func (c *Cache) login(url, username, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"userName": username, "pwd": password})
	if err != nil {
		return "", err
	}

	authorizeUrl := strings.TrimRight(url, "/") + "/api/v2/authorize"
	req, err := http.NewRequest(http.MethodPost, authorizeUrl, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Kubex authorization failed with status %d (%s)", resp.StatusCode, resp.Status)
	}

	var parsed struct {
		ApiToken string `json:"apiToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.ApiToken == "" {
		return "", fmt.Errorf("Kubex authorization response did not contain an apiToken")
	}

	return parsed.ApiToken, nil
}

// FetchClusters calls Kubex's REST "List Kubernetes clusters" endpoint
// (GET {url}/api/v2/kubernetes/clusters) with token as the Bearer
// credential, and returns the raw JSON response body. This is the REST
// equivalent of the "kubex-cluster-connections" MCP tool, used because
// this package's login token isn't accepted by the MCP server (see the
// package doc comment) — the field names differ slightly from the MCP
// tool's response, and there's no live connector "status" field in this
// data at all, which callers should account for.
func FetchClusters(client *http.Client, url, token string) (string, error) {
	clustersUrl := strings.TrimRight(url, "/") + "/api/v2/kubernetes/clusters"
	req, err := http.NewRequest(http.MethodGet, clustersUrl, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Kubex clusters request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}
