package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"kubexhealthcheck/internal/webhook"
)

type webhookRoutineDto struct {
	Url          string  `json:"url"`
	UpdatedAtUtc *string `json:"updatedAtUtc"`
}

func (s *Server) handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	routine, err := s.webhookStore.Get()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toWebhookRoutineDto(routine))
}

type updateWebhookRoutineRequest struct {
	Url string `json:"url"`
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	var req updateWebhookRoutineRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "A valid http or https webhook URL is required."})
		return
	}

	trimmed := strings.TrimSpace(req.Url)
	parsed, err := url.ParseRequestURI(trimmed)
	if trimmed == "" || err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "A valid http or https webhook URL is required."})
		return
	}

	routine, err := s.webhookStore.Save(trimmed)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toWebhookRoutineDto(routine))
}

type sendWebhookMessageRequest struct {
	Message string `json:"message"`
}

func (s *Server) handleSendWebhookMessage(w http.ResponseWriter, r *http.Request) {
	var req sendWebhookMessageRequest
	_ = decodeJSON(r, &req)

	message := strings.TrimSpace(req.Message)
	if message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "A message is required."})
		return
	}

	routine, err := s.webhookStore.Get()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	if routine.Url == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "No webhook URL has been configured yet."})
		return
	}

	if err := s.webhookSender.Send(routine.Url, message); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "Failed to deliver webhook message: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Message sent."})
}

func toWebhookRoutineDto(r webhook.Routine) webhookRoutineDto {
	dto := webhookRoutineDto{Url: r.Url}
	if r.UpdatedAtUtc != nil {
		formatted := r.UpdatedAtUtc.Format(time.RFC3339)
		dto.UpdatedAtUtc = &formatted
	}
	return dto
}
