package api

import (
	"net/http"
	"strings"
)

type claudeCommandRequest struct {
	Command string `json:"command"`
}

func (s *Server) handleClaudeCommand(w http.ResponseWriter, r *http.Request) {
	var req claudeCommandRequest
	_ = decodeJSON(r, &req)

	command := strings.TrimSpace(req.Command)
	if command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "A command is required."})
		return
	}

	response, err := s.claudeService.RunCommand(command)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "Claude request failed: " + err.Error()})
		return
	}

	postedToTeams := false
	var postError *string

	webhookUrl := strings.TrimSpace(s.cfg.WebhookSettings.DefaultUrl)
	if webhookUrl == "" {
		msg := "No webhook URL has been configured (WebhookSettings:DefaultUrl)."
		postError = &msg
	} else if err := s.webhookSender.Send(webhookUrl, response); err != nil {
		msg := "Failed to post to webhook: " + err.Error()
		postError = &msg
	} else {
		postedToTeams = true
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"response":      response,
		"postedToTeams": postedToTeams,
		"postError":     postError,
	})
}
