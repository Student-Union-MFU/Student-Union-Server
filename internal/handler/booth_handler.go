package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"su-server/internal/model"
	"su-server/internal/service"
)

type BoothHandler struct {
	service *service.BoothService
}

func NewBoothHandler(service *service.BoothService) *BoothHandler {
	return &BoothHandler{service: service}
}

func (h *BoothHandler) GetAllBooths(w http.ResponseWriter, r *http.Request) {
	booths, err := h.service.GetAllBooths(r.Context())
	if err != nil {
		// Deliberately not err.Error() in the body, unlike GetAllEvents: a
		// database message on a student's screen helps nobody and describes
		// the server's internals to anyone who asks.
		slog.Error("failed to list booths", "err", err)
		http.Error(w, "failed to list booths", http.StatusInternalServerError)
		return
	}

	// `make`, not `var` — see the repository.
	public := make([]model.PublicBooth, 0, len(booths))
	for _, booth := range booths {
		public = append(public, model.NewPublicBooth(booth))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(public)
}
