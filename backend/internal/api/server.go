// Package api wires up the HTTP endpoints: the API-key auth gate, CORS
// for the static frontend, and the handler for /api/claude/command.
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"kubexhealthcheck/internal/claude"
	"kubexhealthcheck/internal/config"
	"kubexhealthcheck/internal/webhook"
)

const apiKeyHeader = "X-Api-Key"

type Server struct {
	cfg           *config.Config
	claudeService *claude.Service
	webhookSender *webhook.Sender
	mux           *http.ServeMux
}

func NewServer(cfg *config.Config, claudeService *claude.Service, webhookSender *webhook.Sender) *Server {
	s := &Server{
		cfg:           cfg,
		claudeService: claudeService,
		webhookSender: webhookSender,
		mux:           http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.withCORS(s.mux).ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /api/claude/command", s.withApiKey(s.handleClaudeCommand))
}

// handleHealthz is unauthenticated on purpose — it's only used to check
// whether the process is up and listening (e.g. GitHub Actions polling
// after starting the backend), not to expose anything sensitive.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// The frontend is a static file opened directly (file://), which sends
// Origin: null — so any-origin is required rather than an allowlist.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withApiKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		configuredKey := s.cfg.ApiKeySettings.Key
		if strings.TrimSpace(configuredKey) == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"message": "Server is missing ApiKeySettings:Key configuration.",
			})
			return
		}

		if r.Header.Get(apiKeyHeader) != configuredKey {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"message": "Missing or invalid " + apiKeyHeader + " header.",
			})
			return
		}

		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
