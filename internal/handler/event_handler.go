// Package handler is a controller for handling the requests
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"su-server/internal/model"
	"su-server/internal/service"

	"github.com/go-chi/chi/v5"
)

type EventHandler struct {
	service *service.EventService
}

type Response struct {
	Message string `json:"message"`
	Status  bool   `json:"status"`
}

// NewEventHandler is the constructor
func NewEventHandler(service *service.EventService) *EventHandler {
	return &EventHandler{service: service}
}

func (h *EventHandler) GetAllEvents(w http.ResponseWriter, r *http.Request) {
	events, err := h.service.GetAllEvents(r.Context())

	if err != nil {
		http.Error(w, "Failed to get Event Objects: "+err.Error(), http.StatusInternalServerError)
		slog.Error("Error Information", " = ", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

func (h *EventHandler) GetOneEvents(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Failed to get Event Object", http.StatusInternalServerError)
		slog.Error("Error Information", " = ", err)
		return
	}

	event, err := h.service.GetOneEvent(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to get Event Object", http.StatusInternalServerError)
		slog.Error("Error Information", " = ", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(event)
}

func (h *EventHandler) CreateOneEvent(w http.ResponseWriter, r *http.Request) {
	var event model.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		slog.Error("Error Information", " = ", err)
		return
	}

	completed, err := h.service.CreateOneEvent(r.Context(), event)
	if err != nil {
		http.Error(w, "Failed to create event: "+err.Error(), http.StatusInternalServerError)
		slog.Error("Error Information", " = ", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Message: "Event Created",
		Status:  completed,
	})
}

func (h *EventHandler) UpdateOneEvent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))

	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		slog.Error("Error Information", " = ", err)
		return
	}

	var event model.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		slog.Error("Error Information", " = ", err)
		return
	}

	completed, err := h.service.UpdateOneEvent(r.Context(), id, event)
	if err != nil {
		http.Error(w, "Failed to update the event: "+err.Error(), http.StatusInternalServerError)
		slog.Error("Error Information", " = ", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(completed)
}

func (h *EventHandler) DeleteOneEvents(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Failed to get Event Object", http.StatusInternalServerError)
		slog.Error("Error Information", " = ", err)
		return
	}

	event, err := h.service.DeleteEvent(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to get Event Object", http.StatusInternalServerError)
		slog.Error("Error Information", " = ", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(event)
}
