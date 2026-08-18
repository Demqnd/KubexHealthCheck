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

	routine, err := s.webhookStore.Get()
	if err != nil || routine.Url == "" {
		msg := "No webhook URL has been configured yet."
		postError = &msg
	} else if err := s.webhookSender.Send(routine.Url, response); err != nil {
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

type askClaudeRequest struct {
	ApiKey      string `json:"apiKey"`
	Question    string `json:"question"`
	PostToTeams bool   `json:"postToTeams"`
}

func (s *Server) handleClaudeAsk(w http.ResponseWriter, r *http.Request) {
	var req askClaudeRequest
	_ = decodeJSON(r, &req)

	apiKey := strings.TrimSpace(req.ApiKey)
	question := strings.TrimSpace(req.Question)

	if apiKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "An Anthropic API key is required."})
		return
	}
	if question == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "A question is required."})
		return
	}

	answer, err := s.claudeService.Ask(apiKey, question)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "Claude request failed: " + err.Error()})
		return
	}

	postedToTeams := false
	var postError *string

	if req.PostToTeams {
		routine, err := s.webhookStore.Get()
		if err != nil || routine.Url == "" {
			msg := "No webhook URL has been configured yet."
			postError = &msg
		} else if err := s.webhookSender.Send(routine.Url, answer); err != nil {
			msg := "Failed to post to webhook: " + err.Error()
			postError = &msg
		} else {
			postedToTeams = true
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"answer":        answer,
		"postedToTeams": postedToTeams,
		"postError":     postError,
	})
}
