// Package kubexauth signs in to Kubex with a username/password to get a
// bearer token, instead of relying on a manually-obtained MCP OAuth
// token that expires quickly. This calls Kubex's plain REST login
// endpoint (POST {url}/api/v2/authorize), the same one the old
// KubexHealthCheckService used before this project moved to MCP — it is
// NOT the MCP server's own OAuth 2.1 flow. Whether the resulting token
// is actually accepted as a bearer token by the MCP endpoint itself is
// unverified; this is worth testing directly.
package kubexauth

import (
	"bytes"
	"encoding/json"
	"fmt"
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
